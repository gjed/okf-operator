package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	gogithub "github.com/google/go-github/v93/github"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gjed/okf-operator/api/v1alpha1"
	"github.com/gjed/okf-operator/internal/approve"
	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/propose"
	"github.com/gjed/okf-operator/internal/reconcile"
	"github.com/gjed/okf-operator/internal/review"
	"github.com/gjed/okf-operator/internal/routing"
	"github.com/gjed/okf-operator/internal/write"
)

// Limits of one write and review run, the CLI defaults.
const (
	maxPages         = 10
	maxTokensPerPage = 16000
)

// Runner is the in-process Pipeline: it calls the stage packages directly,
// never the CLI.
//
// SIMPLIFIED: every stage runs in the controller process, so the Pod holds
// both Apps' keys and both model keys. Upgrade path: a Job per run, with the
// review stages in their own Job, when each step needs separate Secrets.
type Runner struct {
	// Secrets reads Secrets, uncached.
	Secrets client.Reader
	// APIURL is the GitHub API base URL.
	APIURL string
	// Checkout materialises the knowledge checkout.
	Checkout Checkout
	// TempDir holds the per-run checkouts; "" is os.TempDir.
	TempDir string
	// ModelHTTP is the model clients' HTTP client; nil is the model default.
	ModelHTTP *http.Client
	// MergePoll bounds the reads that wait for GitHub to compute a bot PR's
	// merge state and to move its branch; the zero value is github.Poll's default.
	MergePoll   github.Poll
	ToolVersion string
	Logger      *slog.Logger
	Now         func() time.Time

	gated gatedPlans
}

// gatedPlans remembers, per knowledge repo and source, the plan behind the
// bot PR head the controller last pushed, so later runs that push nothing
// can gate that head again.
//
// SIMPLIFIED: kept in memory, so after a restart a head is gated again only
// once a run pushes to it. Upgrade path: store the plan on kb-state.
type gatedPlans struct {
	mu sync.Mutex
	m  map[string]gatedPlan
}

type gatedPlan struct {
	head string
	plan *plan.Plan
}

func (g *gatedPlans) put(key, head string, p *plan.Plan) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		g.m = map[string]gatedPlan{}
	}
	g.m[key] = gatedPlan{head: head, plan: p}
}

// get returns the plan behind head, if head is the one last pushed.
func (g *gatedPlans) get(key, head string) *plan.Plan {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gp, ok := g.m[key]; ok && gp.head == head {
		return gp.plan
	}
	return nil
}

// run is the state of one Run.
type run struct {
	*Runner
	job     Job
	secrets *github.Secrets
	logger  *slog.Logger
	writer  github.Config
	kbRepo  string
	source  string
	out     Outcome
}

// fail wraps err as a RunError of stage with every known secret redacted.
func (r *run) fail(stage string, err error) error {
	return &RunError{Stage: stage, Msg: r.secrets.Redact(err.Error())}
}

// stage runs fn in a child span named after stage, so the span annotations
// of the stage packages land on it as they land on a CLI command's span.
func stage[T any](ctx context.Context, r *run, name string, fn func(context.Context) (T, error)) (T, error) {
	ctx, span := otel.Tracer("okf-operator").Start(ctx, name)
	defer span.End()
	v, err := fn(ctx)
	if err != nil {
		err = r.fail(name, err)
		span.SetStatus(codes.Error, err.Error())
	}
	return v, err
}

// Run implements Pipeline.
func (rn *Runner) Run(ctx context.Context, job Job) (Outcome, error) {
	secrets := &github.Secrets{}
	r := &run{
		Runner:  rn,
		job:     job,
		secrets: secrets,
		logger: slog.New(github.NewRedactingHandler(rn.Logger.Handler(), secrets)).With(
			"source", job.Source.Spec.Repository, "knowledge_repo", job.KnowledgeBase.Spec.Repository, "mode", job.Mode),
		kbRepo: job.KnowledgeBase.Spec.Repository,
		source: job.Source.Spec.Repository,
	}
	err := r.pipeline(ctx)
	return r.out, err
}

func (r *run) provider(cfg github.Config, a github.Access) (*github.Provider, error) {
	return github.NewProvider(cfg, a, r.secrets, r.logger)
}

// botLogin resolves an App's bot user; a failure only costs the webhook's
// bot filter, so it is logged and ignored.
func (r *run) botLogin(ctx context.Context, cfg github.Config) string {
	p, err := r.provider(cfg, github.Access{})
	if err == nil {
		var slug string
		if slug, err = p.AppSlug(ctx); err == nil {
			return slug + "[bot]"
		}
	}
	r.logger.WarnContext(ctx, "resolve App bot login", "err", r.secrets.Redact(err.Error()))
	return ""
}

func (r *run) pipeline(ctx context.Context) error {
	kb, ns := r.job.KnowledgeBase, r.job.Source.Namespace
	var err error
	if r.writer, err = appConfig(ctx, r.Secrets, ns, kb.Spec.Writer.AppSecretRef, r.APIURL, r.secrets); err != nil {
		return r.fail(StageCredentials, err)
	}
	writerKey, err := modelKey(ctx, r.Secrets, ns, kb.Spec.Writer.Model.APIKeySecretRef, r.secrets)
	if err != nil {
		return r.fail(StageCredentials, err)
	}
	if login := r.botLogin(ctx, r.writer); login != "" {
		r.out.BotLogins = append(r.out.BotLogins, login)
	}

	// One token per repo and role: the source repo never sees a write
	// permission, even when both repos share an owner.
	kbRead, err := r.provider(r.writer, readPRs(r.kbRepo))
	if err != nil {
		return r.fail(StageCredentials, err)
	}
	kbWrite, err := r.provider(r.writer, knowledgeAccess(r.kbRepo))
	if err != nil {
		return r.fail(StageCredentials, err)
	}
	srcRead, err := r.provider(r.writer, readPRs(r.source))
	if err != nil {
		return r.fail(StageCredentials, err)
	}

	if _, err := stage(ctx, r, StageDeclined, func(ctx context.Context) (int, error) {
		n, err := recordDeclined(ctx, kbWrite, r.kbRepo, r.source)
		if n > 0 {
			r.logger.InfoContext(ctx, "recorded declined bot PRs", "prs", n)
		}
		return n, err
	}); err != nil {
		return err
	}

	dir, err := os.MkdirTemp(r.TempDir, "okf-run-")
	if err != nil {
		return r.fail(StageCheckout, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	knowledge := propose.GitHubKnowledge{Clients: kbRead, Repo: r.kbRepo}
	repairs := propose.GitHubKnowledge{Clients: kbWrite, Repo: r.kbRepo, Poll: r.MergePoll}
	key := strings.ToLower(r.kbRepo + " " + r.source)
	// head is where the run builds: base is the open bot PR's head (after an
	// update-branch), else the default branch head. A conflicting PR is
	// checked out only once it is repaired, so base is its old head until then.
	// repaired is true when this run moved the PR's head, by update-branch or
	// by the regeneration commit.
	type head struct {
		defBranch, defHead, base string
		pull                     *propose.Pull
		dirty, repaired          bool
	}
	h, err := stage(ctx, r, StageCheckout, func(ctx context.Context) (head, error) {
		defBranch, defHead, err := knowledge.DefaultBranch(ctx)
		if err != nil {
			return head{}, err
		}
		owner, _, _ := strings.Cut(r.kbRepo, "/")
		pull, err := knowledge.OpenPull(ctx, owner+":"+propose.Branch(r.source), defBranch)
		if err != nil {
			return head{}, err
		}
		h := head{defBranch: defBranch, defHead: defHead, base: defHead, pull: pull}
		if pull != nil {
			r.out.PR = &v1alpha1.PullRequest{Number: pull.Number, URL: pull.URL}
			synced, err := propose.SyncPull(ctx, repairs, r.source, pull)
			if err != nil {
				return head{}, err
			}
			r.logger.InfoContext(ctx, "bot PR merge state", "pr", pull.Number, "state", synced.State)
			h.base, h.dirty = synced.Head, synced.Dirty
			if synced.Head != pull.HeadSHA {
				h.repaired = true
				r.logger.InfoContext(ctx, "bot PR branch updated", "pr", pull.Number, "from", pull.HeadSHA, "to", synced.Head)
				// The plan behind the old head still describes the new one,
				// which only merges the default branch in.
				if gp := r.gated.get(key, pull.HeadSHA); gp != nil {
					r.gated.put(key, synced.Head, gp)
				}
			}
		}
		return h, nil
	})
	if err != nil {
		return err
	}
	if !h.dirty {
		// A moved head, whether this run's update-branch or an earlier run's
		// repair that failed afterwards, leaves approvals of the old head
		// behind. They go before anything else can fail.
		if err := r.dismissSuperseded(ctx, h.pull, h.base); err != nil {
			return err
		}
		if _, err := stage(ctx, r, StageCheckout, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, r.Checkout.Checkout(ctx, kbRead, r.kbRepo, h.base, dir)
		}); err != nil {
			return err
		}
	}

	// The routing map always comes from the default branch: a bot branch's
	// copy is as old as its PR and would miss routes added since.
	rm, err := stage(ctx, r, StageRouting, func(ctx context.Context) (*routing.Map, error) {
		data, found, err := gate.GitHubReader{Clients: kbRead}.File(ctx, r.kbRepo, h.defHead, routing.DefaultPath)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%s is missing from %s@%s", routing.DefaultPath, r.kbRepo, h.defHead)
		}
		m, err := routing.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("routing map %s: %w", routing.DefaultPath, err)
		}
		return m, nil
	})
	if err != nil {
		return err
	}

	cur := cursor.Reader{Clients: kbRead, KnowledgeRepo: r.kbRepo}
	differ := plan.GitHubDiffer{Clients: srcRead}
	if h.dirty {
		repairDir := ""
		defer func() {
			if repairDir != "" {
				_ = os.RemoveAll(repairDir)
			}
		}()
		rep, err := stage(ctx, r, StageRepair, func(ctx context.Context) (*propose.Repaired, error) {
			rep, err := propose.Repair(ctx, propose.RepairInput{
				Knowledge: repairs, Source: r.source, Pull: h.pull, DefBranch: h.defBranch, DefHead: h.defHead, Logger: r.logger,
				Regenerate: func(ctx context.Context, conflicts []string, from, to string) (*propose.Regenerated, error) {
					var err error
					if repairDir, err = os.MkdirTemp(r.TempDir, "okf-repair-"); err != nil {
						return nil, err
					}
					if err := r.Checkout.Checkout(ctx, kbRead, r.kbRepo, h.defHead, repairDir); err != nil {
						return nil, err
					}
					return r.regenerate(ctx, rm, differ, writerKey, repairDir, conflicts, from, to)
				},
			})
			return rep, err
		})
		if err != nil {
			return err
		}
		if err := r.dismissSuperseded(ctx, h.pull, rep.Head); err != nil {
			return err
		}
		if _, err := stage(ctx, r, StageCheckout, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, r.Checkout.Checkout(ctx, kbRead, r.kbRepo, rep.Head, dir)
		}); err != nil {
			return err
		}
		p := rep.Plan
		if p == nil {
			p = r.gated.get(key, h.pull.HeadSHA)
		}
		if p != nil {
			r.gated.put(key, rep.Head, p)
		}
		h.base, h.repaired = rep.Head, true
	}
	var p *plan.Plan
	if r.job.Mode == v1alpha1.ModeReconcile {
		p, err = stage(ctx, r, StageReconcile, func(ctx context.Context) (*plan.Plan, error) {
			return reconcile.Build(ctx, r.source, "", reconcile.Deps{
				Routing: rm, Cursor: cur, Differ: differ, Tree: reconcile.GitHubTree{Clients: srcRead},
				KnowledgeDir: dir, Logger: r.logger,
			})
		})
	} else {
		p, err = stage(ctx, r, StagePlan, func(ctx context.Context) (*plan.Plan, error) {
			return plan.Build(ctx, r.source, "", plan.Deps{
				Routing: rm, Cursor: cur, Differ: differ, Pages: plan.DirPages(dir), Logger: r.logger,
			})
		})
	}
	if err != nil {
		return err
	}

	rep, err := stage(ctx, r, StageWrite, func(ctx context.Context) (*write.Report, error) {
		return write.Run(ctx, p, r.writeDeps(differ, writerKey, dir))
	})
	if err != nil {
		return err
	}

	res, err := stage(ctx, r, StagePropose, func(ctx context.Context) (*propose.Result, error) {
		return propose.Run(ctx, p, rep, propose.Deps{
			Knowledge:     propose.GitHubKnowledge{Clients: kbWrite, Repo: r.kbRepo},
			SourcePRs:     propose.GitHubSourcePRs{Differ: differ},
			Cursor:        cursor.Writer{Clients: kbWrite, KnowledgeRepo: r.kbRepo},
			KnowledgeRepo: r.kbRepo,
			KnowledgeDir:  dir,
			Base:          h.base,
			Logger:        r.logger,
		})
	})
	if err != nil {
		return err
	}
	r.out.Cursor, r.out.AtHead = p.From, true
	if res.CursorAdvanced {
		r.out.Cursor = res.To
	}
	if res.PR != nil {
		r.out.PR = &v1alpha1.PullRequest{Number: res.PR.Number, URL: res.PR.URL}
	}
	r.out.Proposed = res.Action == propose.ActionOpened || res.Action == propose.ActionUpdated
	if !r.out.Proposed {
		// Nothing was pushed: gate the head with the plan behind it, so a
		// policy change on the base dismisses an approval that no longer
		// holds. A head this run's repair moved lost the approval of the old
		// head, so it goes through review and approval again, with the plan
		// of the whole range.
		if gp := r.gated.get(key, h.base); gp != nil && r.out.PR != nil {
			s := &githubPRStages{run: r, pr: r.out.PR.Number, plan: gp, kbRead: kbRead, srcRead: srcRead}
			if h.repaired {
				r.logger.InfoContext(ctx, "reviewing the repaired bot PR head", "pr", s.pr, "head_sha", h.base)
				return gateAndApprove(ctx, s, &r.out)
			}
			return regate(ctx, s, &r.out)
		}
		return nil
	}
	// A reconcile PR always goes to a human.
	if p.Version != plan.Version || res.PR == nil {
		return nil
	}
	r.gated.put(key, res.Commit, p)
	return gateAndApprove(ctx, &githubPRStages{run: r, pr: res.PR.Number, plan: p, kbRead: kbRead, srcRead: srcRead}, &r.out)
}

// dismissSuperseded dismisses the review App's approvals of every commit of
// the open bot PR but head, its current head. It does nothing without a PR.
// Every run does it, so an approval left by a run that failed after it moved
// the head, or by a failed dismissal, is cleared by the next run.
func (r *run) dismissSuperseded(ctx context.Context, pull *propose.Pull, head string) error {
	if pull == nil {
		return nil
	}
	n, err := (&githubPRStages{run: r, pr: pull.Number}).DismissExcept(ctx, head, dismissSuperseded)
	r.out.Dismissed += n
	return err
}

// regate runs the deterministic gate on a head the controller gated before
// and dismisses the review App's approvals of it when the gate now fails. It
// never reviews or approves again.
func regate(ctx context.Context, s prStages, out *Outcome) error {
	rep, err := s.Gate(ctx, nil)
	if err != nil || rep.Pass {
		return err
	}
	n, err := s.Dismiss(ctx, rep.HeadSHA, dismissGateFailed)
	out.Dismissed += n
	return err
}

// writeDeps are the inputs of write.Run on the checkout in dir.
func (r *run) writeDeps(differ plan.GitHubDiffer, writerKey, dir string) write.Deps {
	m := r.job.KnowledgeBase.Spec.Writer.Model
	return write.Deps{
		Source:           differ,
		Model:            r.model(m, writerKey),
		Validator:        write.BaseValidator{},
		KnowledgeDir:     dir,
		ModelName:        m.Name,
		ToolVersion:      r.ToolVersion,
		MaxPages:         maxPages,
		MaxTokensPerPage: maxTokensPerPage,
		Now:              r.Now,
		Logger:           r.logger,
	}
}

// fixedCursor is a plan.CursorReader that holds one commit, so a plan can be
// built over a range other than the real cursor's. It never writes.
type fixedCursor string

func (c fixedCursor) Read(context.Context, string) (string, bool, error) { return string(c), true, nil }

// regenerate runs plan and write over the source range from..to for the
// conflicting pages only, in the checkout of the default branch head in dir.
// The routing map is the default branch's, so the plan covers the pages the
// range routes today. It returns the plan of the whole range as well, which
// is the plan behind the repaired head.
func (r *run) regenerate(ctx context.Context, rm *routing.Map, differ plan.GitHubDiffer, writerKey, dir string, conflicts []string, from, to string) (*propose.Regenerated, error) {
	full, err := plan.Build(ctx, r.source, to, plan.Deps{
		Routing: rm, Cursor: fixedCursor(from), Differ: differ, Pages: plan.DirPages(dir), Logger: r.logger,
	})
	if err != nil {
		return nil, err
	}
	sub := *full
	sub.Pages = nil
	for _, pg := range full.Pages {
		if slices.Contains(conflicts, pg.Path) {
			sub.Pages = append(sub.Pages, pg)
		}
	}
	rep, err := write.Run(ctx, &sub, r.writeDeps(differ, writerKey, dir))
	if err != nil {
		return nil, err
	}
	return &propose.Regenerated{Plan: &sub, Full: full, Report: rep, Dir: dir}, nil
}

func (r *run) model(m v1alpha1.Model, key string) *model.Client {
	return &model.Client{BaseURL: m.BaseURL, Model: m.Name, APIKey: key, HTTP: r.ModelHTTP, Redact: r.secrets.Redact}
}

// prStages are the steps after propose for a version 1 plan.
type prStages interface {
	Gate(ctx context.Context, rv *gate.Review) (*gate.Report, error)
	Review(ctx context.Context) (*gate.Review, error)
	Approve(ctx context.Context, rep *gate.Report) (bool, error)
	Dismiss(ctx context.Context, head, why string) (int, error)
}

// gateAndApprove runs the deterministic gate, then the review, the gate with
// the review and approve. The deterministic gate goes first so a PR that can
// never auto-merge costs no reviewer tokens. Every failing gate dismisses
// the review App's approvals of the evaluated head.
func gateAndApprove(ctx context.Context, s prStages, out *Outcome) error {
	var rv *gate.Review
	for _, withReview := range []bool{false, true} {
		if withReview {
			var err error
			if rv, err = s.Review(ctx); err != nil {
				return err
			}
		}
		rep, err := s.Gate(ctx, rv)
		if err != nil {
			return err
		}
		if !rep.Pass {
			n, err := s.Dismiss(ctx, rep.HeadSHA, dismissGateFailed)
			out.Dismissed += n
			return err
		}
		if withReview {
			ok, err := s.Approve(ctx, rep)
			out.Approved = ok
			return err
		}
	}
	return nil
}

// githubPRStages implements prStages with the stage packages. Gate and review
// read GitHub as the writer App, which can read the source repo; the review
// App, installed on the knowledge repo only, only approves and dismisses.
type githubPRStages struct {
	*run
	pr              int
	plan            *plan.Plan
	kbRead, srcRead github.Clients

	reviewApp   *github.Provider
	reviewLogin string
}

func (g *githubPRStages) Gate(ctx context.Context, rv *gate.Review) (*gate.Report, error) {
	return stage(ctx, g.run, StageGate, func(ctx context.Context) (*gate.Report, error) {
		return gate.Evaluate(ctx, gate.Deps{
			Reader:  gate.GitHubReader{Clients: g.kbRead},
			Commits: gate.GitHubCommits{Clients: g.srcRead},
			Review:  rv,
		}, g.kbRepo, g.pr, g.plan)
	})
}

func (g *githubPRStages) Review(ctx context.Context) (*gate.Review, error) {
	return stage(ctx, g.run, StageReview, func(ctx context.Context) (*gate.Review, error) {
		m := g.job.KnowledgeBase.Spec.Reviewer.Model
		key, err := modelKey(ctx, g.Secrets, g.job.Source.Namespace, m.APIKeySecretRef, g.secrets)
		if err != nil {
			return nil, err
		}
		res, err := review.Run(ctx, review.Deps{
			Reader:           gate.GitHubReader{Clients: g.kbRead},
			Source:           plan.GitHubDiffer{Clients: g.srcRead},
			Model:            g.model(m, key),
			ModelName:        m.Name,
			MaxTokensPerPage: maxTokensPerPage,
			Logger:           g.logger,
		}, g.kbRepo, g.pr, g.plan)
		if err != nil {
			return nil, err
		}
		in := res.GateInput()
		return &in, nil
	})
}

// reviewer loads the review App on first use, after propose.
func (g *githubPRStages) reviewer(ctx context.Context) (*github.Provider, string, error) {
	if g.reviewApp != nil {
		return g.reviewApp, g.reviewLogin, nil
	}
	cfg, err := appConfig(ctx, g.Secrets, g.job.Source.Namespace, g.job.KnowledgeBase.Spec.Reviewer.AppSecretRef, g.APIURL, g.secrets)
	if err != nil {
		return nil, "", err
	}
	p, err := g.provider(cfg, github.Access{
		Repos: []string{g.kbRepo},
		Permissions: gogithub.InstallationPermissions{
			Contents: new("read"), Metadata: new("read"), PullRequests: new("write"),
		},
	})
	if err != nil {
		return nil, "", err
	}
	login := g.botLogin(ctx, cfg)
	if login == "" {
		return nil, "", errors.New("cannot resolve the review App's bot login")
	}
	g.out.BotLogins = append(g.out.BotLogins, login)
	g.reviewApp, g.reviewLogin = p, login
	return p, login, nil
}

func (g *githubPRStages) Approve(ctx context.Context, rep *gate.Report) (bool, error) {
	return stage(ctx, g.run, StageApprove, func(ctx context.Context) (bool, error) {
		p, _, err := g.reviewer(ctx)
		if err != nil {
			return false, err
		}
		out, err := approve.Run(ctx, approve.GitHub{Clients: p}, g.kbRepo, g.pr, rep)
		if err != nil {
			return false, err
		}
		g.logger.InfoContext(ctx, "approval decided", "pr", out.PR, "head_sha", out.HeadSHA, "approved", out.Approved, "reason", out.Reason)
		return out.Approved, nil
	})
}

// DismissExcept dismisses the review App's approvals of every commit but keep.
func (g *githubPRStages) DismissExcept(ctx context.Context, keep, why string) (int, error) {
	return stage(ctx, g.run, StageDismiss, func(ctx context.Context) (int, error) {
		p, login, err := g.reviewer(ctx)
		if err != nil {
			return 0, err
		}
		n, err := dismissExcept(ctx, p, g.kbRepo, g.pr, keep, login, why)
		if n > 0 {
			g.logger.InfoContext(ctx, "dismissed approvals of superseded heads", "pr", g.pr, "head_sha", keep, "dismissed", n)
		}
		return n, err
	})
}

func (g *githubPRStages) Dismiss(ctx context.Context, head, why string) (int, error) {
	return stage(ctx, g.run, StageDismiss, func(ctx context.Context) (int, error) {
		p, login, err := g.reviewer(ctx)
		if err != nil {
			return 0, err
		}
		n, err := dismissStale(ctx, p, g.kbRepo, g.pr, head, login, why)
		if n > 0 {
			g.logger.InfoContext(ctx, "dismissed stale approvals", "pr", g.pr, "head_sha", head, "dismissed", n)
		}
		return n, err
	})
}

// readPRs is read-only contents, metadata and pull requests on one repo.
func readPRs(repo string) github.Access {
	return github.Access{
		Repos: []string{repo},
		Permissions: gogithub.InstallationPermissions{
			Contents: new("read"), Metadata: new("read"), PullRequests: new("read"),
		},
	}
}

// knowledgeAccess is what propose and the declined bookkeeping need on the
// knowledge repo.
func knowledgeAccess(repo string) github.Access {
	return github.Access{
		Repos: []string{repo},
		Permissions: gogithub.InstallationPermissions{
			Contents: new("write"), Metadata: new("read"), PullRequests: new("write"),
		},
	}
}
