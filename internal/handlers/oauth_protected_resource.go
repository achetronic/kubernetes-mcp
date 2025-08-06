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

package handlers

import (
	"encoding/json"
	"net/http"
)

// OauthProtectedResourceResponse represents the response returned by '.well-known/oauth-protected-resource' endpoint
// According to the RFC9728 (Section 2)
// Ref: https://datatracker.ietf.org/doc/rfc9728/
type OauthProtectedResourceResponse struct {

	// Essential: these are commonly included
	Resource                          string   `json:"resource"`                                        // Required
	AuthorizationServers              []string `json:"authorization_servers,omitempty"`                 // Optional
	JwksUri                           string   `json:"jwks_uri,omitempty"`                              // Optional
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`                      // Optional
	BearerMethodsSupported            []string `json:"bearer_methods_supported,omitempty"`              // Optional
	ResourceSigningAlgValuesSupported []string `json:"resource_signing_alg_values_supported,omitempty"` // Optional

	// Extra: these are commonly omitted
	// For reading
	ResourceName          string `json:"resource_name,omitempty"`          // Recommended
	ResourceDocumentation string `json:"resource_documentation,omitempty"` // Optional
	ResourcePolicyUri     string `json:"resource_policy_uri,omitempty"`    // Optional
	ResourceTosUri        string `json:"resource_tos_uri,omitempty"`       // Optional

	// For advanced security
	TlsClientCertificateBoundAccessTokens bool     `json:"tls_client_certificate_bound_access_tokens,omitempty"` // Optional
	AuthorizationDetailsTypesSupported    []string `json:"authorization_details_types_supported,omitempty"`      // Optional
	DpopSigningAlgValuesSupported         []string `json:"dpop_signing_alg_values_supported,omitempty"`          // Optional
	DpopBoundAccessTokensRequired         bool     `json:"dpop_bound_access_tokens_required,omitempty"`          // Optional
}

// HandleOauthProtectedResources process requests for endpoint: /.well-known/oauth-protected-resource
func (h *HandlersManager) HandleOauthProtectedResources(response http.ResponseWriter, request *http.Request) {

	//
	ResponseObject := &OauthProtectedResourceResponse{
		Resource:                              h.dependencies.AppCtx.Config.OAuthProtectedResource.Resource,
		AuthorizationServers:                  h.dependencies.AppCtx.Config.OAuthProtectedResource.AuthServers,
		JwksUri:                               h.dependencies.AppCtx.Config.OAuthProtectedResource.JWKSUri,
		ScopesSupported:                       h.dependencies.AppCtx.Config.OAuthProtectedResource.ScopesSupported,
		BearerMethodsSupported:                h.dependencies.AppCtx.Config.OAuthProtectedResource.BearerMethodsSupported,
		ResourceSigningAlgValuesSupported:     h.dependencies.AppCtx.Config.OAuthProtectedResource.ResourceSigningAlgValuesSupported,
		ResourceName:                          h.dependencies.AppCtx.Config.OAuthProtectedResource.ResourceName,
		ResourceDocumentation:                 h.dependencies.AppCtx.Config.OAuthProtectedResource.ResourceDocumentation,
		ResourcePolicyUri:                     h.dependencies.AppCtx.Config.OAuthProtectedResource.ResourcePolicyUri,
		ResourceTosUri:                        h.dependencies.AppCtx.Config.OAuthProtectedResource.ResourceTosUri,
		TlsClientCertificateBoundAccessTokens: h.dependencies.AppCtx.Config.OAuthProtectedResource.TLSClientCertificateBoundAccessTokens,
		AuthorizationDetailsTypesSupported:    h.dependencies.AppCtx.Config.OAuthProtectedResource.AuthorizationDetailsTypesSupported,
		DpopSigningAlgValuesSupported:         h.dependencies.AppCtx.Config.OAuthProtectedResource.DPoPSigningAlgValuesSupported,
		DpopBoundAccessTokensRequired:         h.dependencies.AppCtx.Config.OAuthProtectedResource.DPoPBoundAccessTokensRequired,
	}

	// Transform into JSON
	ResponseObjectBytes, err := json.Marshal(ResponseObject)
	if err != nil {
		h.dependencies.AppCtx.Logger.Error("error converting response into json", "error", err.Error())
		http.Error(response, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "max-age=3600")
	response.Header().Set("Access-Control-Allow-Origin", "*")
	response.Header().Set("Access-Control-Allow-Methods", "GET")          // FIXME: TOO STRICT
	response.Header().Set("Access-Control-Allow-Headers", "Content-Type") // FIXME: TOO STRICT

	_, err = response.Write(ResponseObjectBytes)
	if err != nil {
		h.dependencies.AppCtx.Logger.Error("error sending response to client", "error", err.Error())
		return
	}
}
