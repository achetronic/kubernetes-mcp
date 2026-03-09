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

const (
	// AuthPayloadHeader is the internal request header where the authenticated payload
	// is stored as hex-encoded JSON. Both JWT and API key middlewares write to this
	// header so that downstream consumers (k8stools, authorization evaluator) can read
	// the payload without caring about the original authentication method.
	AuthPayloadHeader = "X-Auth-Payload"

	// AuthMethodHeader is the internal request header that indicates which authentication
	// method successfully validated the request. It is used to prevent double-processing:
	// if JWT middleware already authenticated the request, the API key middleware skips it.
	AuthMethodHeader = "X-Auth-Method"

	// AuthMethodJWT indicates the request was authenticated via a JSON Web Token
	AuthMethodJWT = "jwt"

	// AuthMethodAPIKey indicates the request was authenticated via a static API key
	// whose payload is defined in the server configuration
	AuthMethodAPIKey = "api_key"
)
