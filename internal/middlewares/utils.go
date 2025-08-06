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

package middlewares

import (
	"net/http"
	"strings"
)

// getRequestScheme returns the scheme of a given request.
// It is inferred from headers and connection in cascade.
func getRequestScheme(req *http.Request) string {

	// Priority 1: X-Forwarded-Proto (Istio/Envoy/Nginx)
	if proto := req.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}

	// Priority 2: Forwarded header (RFC 7239)
	if fwd := req.Header.Get("Forwarded"); fwd != "" {
		if strings.Contains(fwd, "proto=https") {
			return "https"
		}
	}

	// Priority 3: Direct TLS connection
	if req.TLS != nil {
		return "https"
	}

	return "http"
}
