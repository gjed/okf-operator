package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Keys every referenced Secret holds. Secrets are referenced by name only.
const (
	// SecretKeyAppID and SecretKeyPrivateKey are a GitHub App's ID and PEM key.
	SecretKeyAppID      = "app-id"
	SecretKeyPrivateKey = "private-key"
	// SecretKeyAPIKey is a model API key.
	SecretKeyAPIKey = "api-key"
	// SecretKeyWebhook is the GitHub webhook secret.
	SecretKeyWebhook = "secret"
)

// SecretRef names a Secret in the referring object's namespace.
type SecretRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// Model is an OpenAI-compatible model endpoint.
type Model struct {
	// BaseURL is the API base URL, e.g. http://ollama:11434/v1.
	// +kubebuilder:validation:MinLength=1
	BaseURL string `json:"baseURL"`
	// Name is the model name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// APIKeySecretRef names a Secret whose api-key is the model API key.
	// +optional
	APIKeySecretRef *SecretRef `json:"apiKeySecretRef,omitempty"`
}

// Identity is a GitHub App and the model it drives.
type Identity struct {
	// AppSecretRef names a Secret holding the App's app-id and private-key.
	AppSecretRef SecretRef `json:"appSecretRef"`
	// Model is the model this identity uses.
	Model Model `json:"model"`
}

// KnowledgeBaseSpec is a knowledge repository and the credentials that write
// and review it.
type KnowledgeBaseSpec struct {
	// Repository is the knowledge repo, owner/name.
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`
	Repository string `json:"repository"`
	// Writer is the App that plans, writes and proposes, and the writer model.
	Writer Identity `json:"writer"`
	// Reviewer is the review-only App that approves, and the reviewer model.
	Reviewer Identity `json:"reviewer"`
	// WebhookSecretRef names a Secret whose secret verifies GitHub webhooks.
	WebhookSecretRef SecretRef `json:"webhookSecretRef"`
}

// KnowledgeBase is one knowledge repository kept in sync by the controller.
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=kb
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repository`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type KnowledgeBase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec KnowledgeBaseSpec `json:"spec"`
}

// KnowledgeBaseList is a list of KnowledgeBase.
// +kubebuilder:object:root=true
type KnowledgeBaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KnowledgeBase `json:"items"`
}

// KnowledgeBaseRef names a KnowledgeBase in the referring object's namespace.
type KnowledgeBaseRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// SourceSpec is one source repository feeding a knowledge base.
type SourceSpec struct {
	// Repository is the source repo, owner/name.
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`
	Repository string `json:"repository"`
	// KnowledgeBaseRef names the KnowledgeBase this source feeds.
	KnowledgeBaseRef KnowledgeBaseRef `json:"knowledgeBaseRef"`
	// RequeueInterval is the longest time between two runs without a webhook.
	// +kubebuilder:default="1h"
	// +optional
	RequeueInterval *metav1.Duration `json:"requeueInterval,omitempty"`
	// ReconcileInterval is the time between two reconcile runs, weekly by
	// default. 0s disables them.
	// +kubebuilder:default="168h"
	// +optional
	ReconcileInterval *metav1.Duration `json:"reconcileInterval,omitempty"`
}

// PullRequest identifies a knowledge PR.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

// Run modes.
const (
	ModePlan      = "plan"
	ModeReconcile = "reconcile"
)

// ConditionReady is True after a successful run and False after a failed one.
const ConditionReady = "Ready"

// SourceStatus is what the last runs did. The cursor itself lives on the
// knowledge repo's kb-state branch; LastCursor is informational.
type SourceStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// LastRunTime is when the last run finished.
	// +optional
	LastRunTime *metav1.Time `json:"lastRunTime,omitempty"`
	// LastRunMode is plan or reconcile.
	// +optional
	LastRunMode string `json:"lastRunMode,omitempty"`
	// LastCursor is the commit the cursor held after the last successful run.
	// Runs never read it: the cursor on kb-state is the source of truth.
	// +optional
	LastCursor string `json:"lastCursor,omitempty"`
	// LastPR is the knowledge PR the last successful run opened or updated.
	// +optional
	LastPR *PullRequest `json:"lastPR,omitempty"`
	// LastError is the redacted error of the last run, empty after a success.
	// +optional
	LastError string `json:"lastError,omitempty"`
	// LastReconcileTime is when the last successful reconcile run finished.
	// +optional
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`
}

// Source is one source repository the controller keeps a knowledge base in
// sync with.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=okfsrc
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repository`
// +kubebuilder:printcolumn:name="KnowledgeBase",type=string,JSONPath=`.spec.knowledgeBaseRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Cursor",type=string,JSONPath=`.status.lastCursor`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Source struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SourceSpec   `json:"spec"`
	Status SourceStatus `json:"status,omitempty"`
}

// SourceList is a list of Source.
// +kubebuilder:object:root=true
type SourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Source `json:"items"`
}
