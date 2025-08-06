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
	"os"
	"path/filepath"
	"sync"

	"kubernetes-mcp/api"

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
	clients        map[string]*Client
	mutex          sync.RWMutex
	currentContext string
}

// NewClientManager creates a new ClientManager
func NewClientManager(config *api.KubernetesConfig) (*ClientManager, error) {
	cm := &ClientManager{
		config:         config,
		clients:        make(map[string]*Client),
		currentContext: config.DefaultContext,
	}

	// Initialize clients for all configured contexts
	for name, ctxConfig := range config.Contexts {
		client, err := cm.createClient(name, ctxConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create client for context %s: %w", name, err)
		}
		cm.clients[name] = client
	}

	return cm, nil
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
	config, ok := cm.config.Contexts[context]
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
