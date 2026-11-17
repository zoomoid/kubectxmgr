// Package namespace implements live namespace selection for the active shell.
package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/tui"
	"golang.org/x/term"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// New builds namespace selection with the ns alias.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:     "namespace [name]",
		Aliases: []string{"ns"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			disk, err := options.Disk()
			if err != nil {
				return err
			}

			cfg, err := shared.ActiveConfig()
			if err != nil {
				return err
			}

			name := ""
			if len(args) == 1 {
				// Explicit assignment must work without namespace-list permission.
				name = args[0]
			} else {
				name, err = pick(cmd, options, cfg)
				if err != nil {
					return err
				}
			}

			if problems := validation.IsDNS1123Label(name); len(problems) > 0 {
				return fmt.Errorf("invalid namespace: %s", strings.Join(problems, "; "))
			}

			data, err := clientcmd.Write(*cfg)
			if err != nil {
				return err
			}

			data, name, err = kubeconfig.Select(data, cfg.CurrentContext, name)
			if err != nil {
				return err
			}

			return options.Save(cmd, disk, state.Selection{
				Context:    cfg.CurrentContext,
				Namespace:  name,
				Kubeconfig: data,
			})
		},
	}
}

// pick queries every namespace page immediately before opening the picker.
func pick(cmd *cobra.Command, options *shared.Options, cfg *clientcmdapi.Config) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("namespace selection needs a TTY; specify a namespace explicitly")
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return "", err
	}
	restConfig.Timeout = 15 * time.Second

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	var refs []state.Reference
	token := ""
	for {
		result, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 100, Continue: token})
		if err != nil {
			return "", fmt.Errorf("list namespaces (or specify one explicitly): %w", err)
		}

		for _, item := range result.Items {
			refs = append(refs, state.Reference{ID: item.Name, Name: item.Name})
		}

		token = result.Continue
		if token == "" {
			break
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	selected, err := tui.Pick(cmd.Context(), refs, options.Regex)
	if err != nil {
		return "", err
	}

	return selected.Name, nil
}
