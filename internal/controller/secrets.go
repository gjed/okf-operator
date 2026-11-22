package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gjed/okf-operator/api/v1alpha1"
	"github.com/gjed/okf-operator/internal/github"
)

// secretData reads a Secret and registers every value for redaction.
func secretData(ctx context.Context, r client.Reader, ns, name string, secrets *github.Secrets) (map[string][]byte, error) {
	var s corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &s); err != nil {
		return nil, fmt.Errorf("read Secret %s: %w", name, err)
	}
	for _, v := range s.Data {
		secrets.Add(string(v))
	}
	return s.Data, nil
}

// secretValue reads one non-empty key of a Secret.
func secretValue(ctx context.Context, r client.Reader, ns, name, key string, secrets *github.Secrets) ([]byte, error) {
	data, err := secretData(ctx, r, ns, name, secrets)
	if err != nil {
		return nil, err
	}
	v := data[key]
	if len(v) == 0 {
		return nil, fmt.Errorf("secret %s has no %s", name, key)
	}
	return v, nil
}

// appConfig builds a GitHub App config from a Secret holding app-id and
// private-key. The key stays in memory.
func appConfig(ctx context.Context, r client.Reader, ns string, ref v1alpha1.SecretRef, apiURL string, secrets *github.Secrets) (github.Config, error) {
	data, err := secretData(ctx, r, ns, ref.Name, secrets)
	if err != nil {
		return github.Config{}, err
	}
	key := data[v1alpha1.SecretKeyPrivateKey]
	if len(key) == 0 {
		return github.Config{}, fmt.Errorf("secret %s has no %s", ref.Name, v1alpha1.SecretKeyPrivateKey)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(string(data[v1alpha1.SecretKeyAppID])), 10, 64)
	if err != nil || id <= 0 {
		return github.Config{}, fmt.Errorf("secret %s: %s is not a positive integer", ref.Name, v1alpha1.SecretKeyAppID)
	}
	return github.Config{APIURL: apiURL, App: &github.AppConfig{ID: id, Key: key}}, nil
}

// modelKey reads a model API key, or "" when no Secret is referenced.
func modelKey(ctx context.Context, r client.Reader, ns string, ref *v1alpha1.SecretRef, secrets *github.Secrets) (string, error) {
	if ref == nil {
		return "", nil
	}
	v, err := secretValue(ctx, r, ns, ref.Name, v1alpha1.SecretKeyAPIKey, secrets)
	return strings.TrimSpace(string(v)), err
}
