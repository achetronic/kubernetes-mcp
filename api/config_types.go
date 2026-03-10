/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

import "time"

// ServerTransportHTTPConfig represents the HTTP transport configuration
type ServerTransportHTTPConfig struct {
	Host string `yaml:"host"`
}

// ServerTransportConfig represents the transport configuration
type ServerTransportConfig struct {
	Type string                    `yaml:"type"`
	HTTP ServerTransportHTTPConfig `yaml:"http,omitempty"`
}

// ServerConfig represents the server configuration section
type ServerConfig struct {
	Name      string                `yaml:"name"`
	Version   string                `yaml:"version"`
	Transport ServerTransportConfig `yaml:"transport,omitempty"`
}

// AccessLogsConfig represents the AccessLogs middleware configuration
type AccessLogsConfig struct {
	ExcludedHeaders []string `yaml:"excluded_headers"`
	RedactedHeaders []string `yaml:"redacted_headers"`
}

// JWTValidationAllowCondition represents a condition for allowing a request after JWT validation
type JWTValidationAllowCondition struct {
	Expression string `yaml:"expression"`
}

// JWTValidationConfig represents the JWT validation configuration
type JWTValidationConfig struct {
	JWKSUri         string                        `yaml:"jwks_uri"`
	CacheInterval   time.Duration                 `yaml:"cache_interval"`
	AllowConditions []JWTValidationAllowCondition `yaml:"allow_conditions,omitempty"`
}

// JWTConfig represents the JWT middleware configuration
type JWTConfig struct {
	Enabled    bool                `yaml:"enabled"`
	Validation JWTValidationConfig `yaml:"validation,omitempty"`
}

// APIKeyConfig represents a single API key entry
type APIKeyConfig struct {
	Name   string         `yaml:"name"`
	Token  string         `yaml:"token"`
	Payload map[string]any `yaml:"payload"`
}

// APIKeysConfig represents the API keys middleware configuration
type APIKeysConfig struct {
	Enabled bool           `yaml:"enabled"`
	Keys    []APIKeyConfig `yaml:"keys,omitempty"`
}

// MiddlewareConfig represents the middleware configuration section
type MiddlewareConfig struct {
	AccessLogs AccessLogsConfig `yaml:"access_logs"`
	JWT        JWTConfig        `yaml:"jwt,omitempty"`
	APIKeys    APIKeysConfig    `yaml:"api_keys,omitempty"`
}

// OAuthAuthorizationServer represents the OAuth Authorization Server configuration
type OAuthAuthorizationServer struct {
	Enabled   bool   `yaml:"enabled"`
	UrlSuffix string `yaml:"url_suffix,omitempty"`

	IssuerUri string `yaml:"issuer_uri"`
}

// OAuthProtectedResourceConfig represents the OAuth Protected Resource configuration
type OAuthProtectedResourceConfig struct {
	Enabled   bool   `yaml:"enabled"`
	UrlSuffix string `yaml:"url_suffix,omitempty"`

	Resource                              string   `yaml:"resource"`
	AuthServers                           []string `yaml:"auth_servers"`
	JWKSUri                               string   `yaml:"jwks_uri"`
	ScopesSupported                       []string `yaml:"scopes_supported"`
	BearerMethodsSupported                []string `yaml:"bearer_methods_supported,omitempty"`
	ResourceSigningAlgValuesSupported     []string `yaml:"resource_signing_alg_values_supported,omitempty"`
	ResourceName                          string   `yaml:"resource_name,omitempty"`
	ResourceDocumentation                 string   `yaml:"resource_documentation,omitempty"`
	ResourcePolicyUri                     string   `yaml:"resource_policy_uri,omitempty"`
	ResourceTosUri                        string   `yaml:"resource_tos_uri,omitempty"`
	TLSClientCertificateBoundAccessTokens bool     `yaml:"tls_client_certificate_bound_access_tokens,omitempty"`
	AuthorizationDetailsTypesSupported    []string `yaml:"authorization_details_types_supported,omitempty"`
	DPoPSigningAlgValuesSupported         []string `yaml:"dpop_signing_alg_values_supported,omitempty"`
	DPoPBoundAccessTokensRequired         bool     `yaml:"dpop_bound_access_tokens_required,omitempty"`
}

// KubernetesContextConfig represents the configuration for a k8s context
type KubernetesContextConfig struct {
	Name              string   `yaml:"name"`
	Kubeconfig        string   `yaml:"kubeconfig,omitempty"`
	KubeconfigContext string   `yaml:"kubeconfig_context,omitempty"`
	Description       string   `yaml:"description,omitempty"`
	AllowedNamespaces []string `yaml:"allowed_namespaces,omitempty"`
	DeniedNamespaces  []string `yaml:"denied_namespaces,omitempty"`
}

// BulkOperationsConfig represents limits for bulk operations
type BulkOperationsConfig struct {
	MaxResourcesPerOperation int `yaml:"max_resources_per_operation"`
}

// KubernetesToolsConfig represents the tools configuration
type KubernetesToolsConfig struct {
	BulkOperations BulkOperationsConfig `yaml:"bulk_operations,omitempty"`
}

// KubernetesConfig represents the Kubernetes configuration
type KubernetesConfig struct {
	DefaultContext string                    `yaml:"default_context"`
	Contexts       []KubernetesContextConfig `yaml:"contexts,omitempty"`
	ContextsDir    string                    `yaml:"contexts_dir,omitempty"`
	Tools          KubernetesToolsConfig     `yaml:"tools,omitempty"`
}

// MatchConfig represents a match condition for authorization
type MatchConfig struct {
	Expression string `yaml:"expression"`
}

// RuleEffect represents whether a rule allows or denies access
type RuleEffect string

const (
	RuleEffectAllow RuleEffect = "allow"
	RuleEffectDeny  RuleEffect = "deny"
)

// ResourceRule represents a rule for filtering resources by GVR + namespace + name.
// All fields support glob patterns. Omitted fields match everything.
type ResourceRule struct {
	// Groups filters by API group (supports glob)
	// - [""] = Core API only
	// - ["_"] = Virtual MCP resources only
	// - ["*"] or omit = any group
	Groups []string `yaml:"groups,omitempty"`

	// Versions filters by API version (supports glob)
	// - ["*"] or omit = any version
	Versions []string `yaml:"versions,omitempty"`

	// Resources filters by resource name in the API sense (supports glob)
	// e.g. "pods", "deployments", "configmaps"
	// - ["*"] or omit = any resource
	Resources []string `yaml:"resources,omitempty"`

	// Namespaces filters by namespace (supports glob)
	// - omit = any namespace + cluster-scoped
	// - ["*"] = any namespaced resource only
	// - [""] = cluster-scoped only
	Namespaces []string `yaml:"namespaces,omitempty"`

	// Names filters by resource instance name
	// Supports exact match or glob patterns
	Names []string `yaml:"names,omitempty"`
}

// AuthorizationRule represents a single allow or deny rule within a policy
type AuthorizationRule struct {
	Effect    RuleEffect     `yaml:"effect"`
	Tools     []string       `yaml:"tools,omitempty"`
	Contexts  []string       `yaml:"contexts,omitempty"`
	Resources []ResourceRule `yaml:"resources,omitempty"`
}

// AuthorizationPolicy represents an authorization policy
type AuthorizationPolicy struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description,omitempty"`
	Match       MatchConfig         `yaml:"match"`
	Rules       []AuthorizationRule `yaml:"rules"`
}

// AuthorizationConfig represents the authorization configuration
type AuthorizationConfig struct {
	AllowAnonymous bool                  `yaml:"allow_anonymous"`
	Policies       []AuthorizationPolicy `yaml:"policies"`
}

// Configuration represents the complete configuration structure
type Configuration struct {
	Server                   ServerConfig                 `yaml:"server,omitempty"`
	Middleware               MiddlewareConfig             `yaml:"middleware,omitempty"`
	OAuthAuthorizationServer OAuthAuthorizationServer     `yaml:"oauth_authorization_server,omitempty"`
	OAuthProtectedResource   OAuthProtectedResourceConfig `yaml:"oauth_protected_resource,omitempty"`
	Kubernetes               KubernetesConfig             `yaml:"kubernetes,omitempty"`
	Authorization            AuthorizationConfig          `yaml:"authorization,omitempty"`
}
