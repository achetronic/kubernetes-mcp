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

package globals

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"kubernetes-mcp/api"
	"kubernetes-mcp/internal/config"
)

type ApplicationContext struct {
	Context    context.Context
	Logger     *slog.Logger
	Config     *api.Configuration
	ToolPrefix string
}

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

func SanitizeToolPrefix(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonAlphanumRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return ""
	}
	return s + "_"
}

const defaultServerName = "kubernetes-mcp"

func NewApplicationContext() (*ApplicationContext, error) {

	appCtx := &ApplicationContext{
		Context: context.Background(),
		Logger:  slog.New(slog.NewJSONHandler(os.Stderr, nil)),
	}

	// Parse and store the config
	var configFlag = flag.String("config", "config.yaml", "path to the config file")
	flag.Parse()

	configContent, err := config.ReadFile(*configFlag)
	if err != nil {
		return appCtx, err
	}
	appCtx.Config = &configContent
	serverName := configContent.Server.Name
	if serverName == "" {
		serverName = defaultServerName
	}
	appCtx.ToolPrefix = SanitizeToolPrefix(serverName)

	//
	return appCtx, nil
}
