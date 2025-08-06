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
	"kubernetes-mcp/internal/globals"
	"net/http"
	"time"
)

type AccessLogsMiddlewareDependencies struct {
	AppCtx *globals.ApplicationContext
}

type AccessLogsMiddleware struct {
	dependencies AccessLogsMiddlewareDependencies
}

func NewAccessLogsMiddleware(dependencies AccessLogsMiddlewareDependencies) *AccessLogsMiddleware {
	return &AccessLogsMiddleware{
		dependencies: dependencies,
	}
}

func (mw *AccessLogsMiddleware) Middleware(next http.Handler) http.Handler {

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {

		start := time.Now()
		next.ServeHTTP(rw, req)
		duration := time.Since(start)

		filteredHeaders := req.Header.Clone()
		// Redact selected headers
		for _, redactedHeader := range mw.dependencies.AppCtx.Config.Middleware.AccessLogs.RedactedHeaders {
			tmpHeader := filteredHeaders.Get(redactedHeader)

			if len(tmpHeader) >= 10 {
				filteredHeaders.Set(redactedHeader, tmpHeader[:10]+"***")
				continue
			}
			filteredHeaders.Set(redactedHeader, "***")
		}

		// Exclude selected headers
		for _, excludedHeader := range mw.dependencies.AppCtx.Config.Middleware.AccessLogs.ExcludedHeaders {
			filteredHeaders.Del(excludedHeader)
		}

		mw.dependencies.AppCtx.Logger.Info("AccessLogsMiddleware output",
			"method", req.Method,
			"url", req.URL.String(),
			"remote_addr", req.RemoteAddr,
			"user_agent", req.UserAgent(),
			"headers", filteredHeaders,
			"request_duration", duration.String(),
		)
	})
}
