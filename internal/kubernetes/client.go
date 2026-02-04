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

package kubernetes

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kubernetes-mcp/api"

	"github.com/fsnotify/fsnotify"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client holds all the kubernetes clients for a single context
type Client struct {
	Config        *rest.Config
	Clientset     *kubernetes.Clientset
	DynamicClient dynamic.Interface
	MetricsClient *metricsv.Clientset
}

// ClientManager manages multiple kubernetes clients for different contexts
type ClientManager struct {
	config         *api.KubernetesConfig
	contextsByName map[string]api.KubernetesContextConfig
	clients        map[string]*Client
	mutex          sync.RWMutex
	currentContext string

	// File watching
	watcher        *fsnotify.Watcher
	fileToContexts map[string][]string // kubeconfig path -> context names
	stopChan       chan struct{}
}

// NewClientManager creates a new ClientManager
func NewClientManager(config *api.KubernetesConfig) (*ClientManager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	cm := &ClientManager{
		config:         config,
		contextsByName: make(map[string]api.KubernetesContextConfig),
		clients:        make(map[string]*Client),
		currentContext: config.DefaultContext,
		watcher:        watcher,
		fileToContexts: make(map[string][]string),
		stopChan:       make(chan struct{}),
	}

	// Initialize clients for explicit contexts
	for _, ctxConfig := range config.Contexts {
		if _, exists := cm.contextsByName[ctxConfig.Name]; exists {
			return nil, fmt.Errorf("duplicate context name %q in explicit contexts", ctxConfig.Name)
		}
		client, err := cm.createClient(ctxConfig.Name, ctxConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create client for context %s: %w", ctxConfig.Name, err)
		}
		cm.clients[ctxConfig.Name] = client
		cm.contextsByName[ctxConfig.Name] = ctxConfig

		// Track file for watching
		if ctxConfig.Kubeconfig != "" {
			cm.trackFile(ctxConfig.Kubeconfig, ctxConfig.Name)
		}
	}

	// Load contexts from directory if specified
	if config.ContextsDir != "" {
		if err := cm.loadContextsFromDir(config.ContextsDir); err != nil {
			return nil, fmt.Errorf("failed to load contexts from directory %s: %w", config.ContextsDir, err)
		}
	}

	// Start watching for file changes
	go cm.watchFiles()

	return cm, nil
}

// loadContextsFromDir loads kubeconfig files from a directory
func (cm *ClientManager) loadContextsFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		kubeconfigPath := filepath.Join(dir, name)

		// Load kubeconfig to extract current-context
		kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("failed to load kubeconfig %s: %w", kubeconfigPath, err)
		}

		contextName := kubeconfig.CurrentContext
		if contextName == "" {
			return fmt.Errorf("kubeconfig %s has no current-context set", kubeconfigPath)
		}

		// Check for collision
		if existing, exists := cm.contextsByName[contextName]; exists {
			return fmt.Errorf("context name collision: %q already defined (from %s), found again in %s",
				contextName, existing.Kubeconfig, kubeconfigPath)
		}

		ctxConfig := api.KubernetesContextConfig{
			Name:       contextName,
			Kubeconfig: kubeconfigPath,
		}

		client, err := cm.createClient(contextName, ctxConfig)
		if err != nil {
			return fmt.Errorf("failed to create client for context %s from %s: %w", contextName, kubeconfigPath, err)
		}

		cm.clients[contextName] = client
		cm.contextsByName[contextName] = ctxConfig

		// Track file for watching
		cm.trackFile(kubeconfigPath, contextName)
	}

	return nil
}

// trackFile adds a kubeconfig file to the watcher by watching its parent directory
func (cm *ClientManager) trackFile(path string, contextName string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		log.Printf("Warning: failed to get absolute path for %s: %v", path, err)
		absPath = path
	}

	cm.fileToContexts[absPath] = append(cm.fileToContexts[absPath], contextName)

	// Watch the parent directory instead of the file directly
	// This is more robust for atomic writes (temp file + rename)
	dir := filepath.Dir(absPath)
	if err := cm.watcher.Add(dir); err != nil {
		log.Printf("Warning: failed to watch directory %s for kubeconfig %s: %v", dir, absPath, err)
	}
}

// watchFiles watches for kubeconfig file changes and reloads clients
func (cm *ClientManager) watchFiles() {
	// Debounce timer to avoid multiple reloads for rapid changes
	var debounceTimer *time.Timer
	pendingFiles := make(map[string]struct{})
	var pendingMu sync.Mutex

	for {
		select {
		case <-cm.stopChan:
			return

		case event, ok := <-cm.watcher.Events:
			if !ok {
				return
			}

			// Only handle write and create events
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			absPath, _ := filepath.Abs(event.Name)

			// Check if this file is one we're tracking
			cm.mutex.RLock()
			_, tracked := cm.fileToContexts[absPath]
			cm.mutex.RUnlock()

			if !tracked {
				continue
			}

			pendingMu.Lock()
			pendingFiles[absPath] = struct{}{}

			// Debounce: wait 500ms before reloading
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
				pendingMu.Lock()
				filesToReload := make([]string, 0, len(pendingFiles))
				for f := range pendingFiles {
					filesToReload = append(filesToReload, f)
				}
				pendingFiles = make(map[string]struct{})
				pendingMu.Unlock()

				for _, filePath := range filesToReload {
					cm.reloadContextsForFile(filePath)
				}
			})
			pendingMu.Unlock()

		case err, ok := <-cm.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// reloadContextsForFile reloads all contexts that use the given kubeconfig file
func (cm *ClientManager) reloadContextsForFile(filePath string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	contextNames, ok := cm.fileToContexts[filePath]
	if !ok {
		return
	}

	for _, contextName := range contextNames {
		ctxConfig, exists := cm.contextsByName[contextName]
		if !exists {
			continue
		}

		log.Printf("Reloading client for context %q due to kubeconfig change", contextName)

		client, err := cm.createClient(contextName, ctxConfig)
		if err != nil {
			log.Printf("Error reloading client for context %s: %v", contextName, err)
			continue
		}

		cm.clients[contextName] = client
	}
}

// Stop stops the file watcher and cleans up resources
func (cm *ClientManager) Stop() {
	close(cm.stopChan)
	if cm.watcher != nil {
		cm.watcher.Close()
	}
}

// createClient creates a kubernetes client for a given context configuration
func (cm *ClientManager) createClient(name string, ctxConfig api.KubernetesContextConfig) (*Client, error) {
	var restConfig *rest.Config
	var err error

	kubeconfigPath := ctxConfig.Kubeconfig
	if kubeconfigPath == "" {
		// Try default kubeconfig location
		if home := os.Getenv("HOME"); home != "" {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	if kubeconfigPath != "" {
		// Build config from kubeconfig file
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
		configOverrides := &clientcmd.ConfigOverrides{}

		if ctxConfig.KubeconfigContext != "" {
			configOverrides.CurrentContext = ctxConfig.KubeconfigContext
		}

		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		restConfig, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
	} else {
		// Try in-cluster config
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build in-cluster config: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	// Create dynamic client
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create metrics client (may fail if metrics-server is not installed)
	metricsClient, err := metricsv.NewForConfig(restConfig)
	if err != nil {
		// Metrics client is optional, log warning but continue
		metricsClient = nil
	}

	return &Client{
		Config:        restConfig,
		Clientset:     clientset,
		DynamicClient: dynamicClient,
		MetricsClient: metricsClient,
	}, nil
}

// GetClient returns the client for a given context
func (cm *ClientManager) GetClient(context string) (*Client, error) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if context == "" {
		context = cm.currentContext
	}

	client, ok := cm.clients[context]
	if !ok {
		return nil, fmt.Errorf("context %s not found", context)
	}

	return client, nil
}

// GetCurrentContext returns the current context name
func (cm *ClientManager) GetCurrentContext() string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.currentContext
}

// SetCurrentContext sets the current context
func (cm *ClientManager) SetCurrentContext(context string) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if _, ok := cm.clients[context]; !ok {
		return fmt.Errorf("context %s not found", context)
	}

	cm.currentContext = context
	return nil
}

// ListContexts returns all available context names
func (cm *ClientManager) ListContexts() []string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	contexts := make([]string, 0, len(cm.clients))
	for name := range cm.clients {
		contexts = append(contexts, name)
	}
	return contexts
}

// GetContextConfig returns the configuration for a given context
func (cm *ClientManager) GetContextConfig(context string) (api.KubernetesContextConfig, bool) {
	if context == "" {
		context = cm.currentContext
	}
	config, ok := cm.contextsByName[context]
	return config, ok
}

// IsNamespaceAllowed checks if a namespace is allowed for a given context
func (cm *ClientManager) IsNamespaceAllowed(context, namespace string) bool {
	config, ok := cm.GetContextConfig(context)
	if !ok {
		return false
	}

	// Check denied namespaces first (takes priority)
	for _, denied := range config.DeniedNamespaces {
		if denied == namespace {
			return false
		}
	}

	// If allowed namespaces is empty, all namespaces are allowed (except denied)
	if len(config.AllowedNamespaces) == 0 {
		return true
	}

	// Check if namespace is in allowed list
	for _, allowed := range config.AllowedNamespaces {
		if allowed == namespace {
			return true
		}
	}

	return false
}
