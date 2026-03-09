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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kubernetes-mcp/internal/globals"
)

type APIKeyValidationMiddlewareDependencies struct {
	AppCtx *globals.ApplicationContext
}

type APIKeyValidationMiddleware struct {
	dependencies APIKeyValidationMiddlewareDependencies

	hashedKeys []hashedAPIKey
}

type hashedAPIKey struct {
	name       string
	tokenHash  [sha256.Size]byte
	payloadJSON []byte
}

func NewAPIKeyValidationMiddleware(deps APIKeyValidationMiddlewareDependencies) (*APIKeyValidationMiddleware, error) {

	mw := &APIKeyValidationMiddleware{
		dependencies: deps,
	}

	for _, key := range deps.AppCtx.Config.Middleware.APIKeys.Keys {
		payloadJSON, err := json.Marshal(key.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload for API key %s: %w", key.Name, err)
		}

		mw.hashedKeys = append(mw.hashedKeys, hashedAPIKey{
			name:        key.Name,
			tokenHash:   sha256.Sum256([]byte(key.Token)),
			payloadJSON: payloadJSON,
		})
	}

	return mw, nil
}

func (mw *APIKeyValidationMiddleware) Authenticate(token string) (name string, payloadJSON []byte, ok bool) {
	tokenHash := sha256.Sum256([]byte(token))

	for _, key := range mw.hashedKeys {
		if subtle.ConstantTimeCompare(tokenHash[:], key.tokenHash[:]) == 1 {
			return key.name, key.payloadJSON, true
		}
	}

	return "", nil, false
}

func (mw *APIKeyValidationMiddleware) Middleware(next http.Handler) http.Handler {

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {

		if !mw.dependencies.AppCtx.Config.Middleware.APIKeys.Enabled {
			next.ServeHTTP(rw, req)
			return
		}

		if req.Header.Get(AuthMethodHeader) != "" {
			next.ServeHTTP(rw, req)
			return
		}

		authHeader := req.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			next.ServeHTTP(rw, req)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		name, payloadJSON, ok := mw.Authenticate(token)
		if !ok {
			next.ServeHTTP(rw, req)
			return
		}

		mw.dependencies.AppCtx.Logger.Info("API key authenticated", "key_name", name)

		req.Header.Set(AuthPayloadHeader, hex.EncodeToString(payloadJSON))
		req.Header.Set(AuthMethodHeader, AuthMethodAPIKey)

		rw.Header().Del("WWW-Authenticate")
		next.ServeHTTP(rw, req)
	})
}
