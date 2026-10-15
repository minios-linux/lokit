package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/openai"
	"github.com/minios-linux/lokit/settings"
	"github.com/spf13/cobra"
)

func newModelsCmd() *cobra.Command {
	var provider, apiKey, proxy string
	var asJSON bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "models",
		Short: T("List models available through your OpenAI authentication"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider != "openai" {
				return fmt.Errorf("model discovery currently supports --provider openai")
			}
			key := settings.ResolveAPIKey("openai", apiKey)
			transport := http.DefaultTransport.(*http.Transport).Clone()
			if proxy != "" {
				parsed, err := url.Parse(proxy)
				if err != nil || parsed.Scheme == "" || parsed.Host == "" {
					return fmt.Errorf("invalid HTTP/HTTPS proxy URL")
				}
				transport.Proxy = http.ProxyURL(parsed)
			}
			models, err := openai.ListModels(cmd.Context(), &http.Client{Transport: transport, Timeout: timeout}, key)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(models)
			}
			for _, model := range models {
				fmt.Fprintln(cmd.OutOrStdout(), model.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "openai", T("AI provider"))
	cmd.Flags().StringVar(&apiKey, "api-key", "", T("API key"))
	cmd.Flags().StringVar(&proxy, "proxy", "", T("HTTP/HTTPS proxy URL"))
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, T("Request timeout"))
	cmd.Flags().BoolVar(&asJSON, "json", false, T("Output JSON"))
	return cmd
}
