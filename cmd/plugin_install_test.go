package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-hulk/clime/internal/plugin"
	"github.com/stretchr/testify/require"
)

func TestEnsureInstallNameAvailable(t *testing.T) {
	t.Parallel()

	manifest := &plugin.Manifest{
		Plugins: []plugin.ManifestEntry{
			{Name: "account", Version: "1.0.0"},
		},
	}

	err := ensureInstallNameAvailable(manifest, "account")
	require.ErrorContains(t, err, "already exists", "expected conflict error for existing plugin name")
	require.ErrorContains(t, err, "clime plugin update account")
	require.NoError(t, ensureInstallNameAvailable(manifest, "opencli"))
}

func TestPluginFromInitSource(t *testing.T) {
	const pluginList = `plugins:
  - name: itsm
    description: ITSM tickets
    script: https://example.com/itsm/install.sh
    binary_path: ~/.local/bin/itsm
`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/plugins.yaml" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(pluginList))
	}))
	defer server.Close()

	listedItsm := plugin.Plugin{
		Name:        "itsm",
		Description: "ITSM tickets",
		Script:      "https://example.com/itsm/install.sh",
		BinaryPath:  "~/.local/bin/itsm",
	}

	tests := []struct {
		name       string
		initURL    string
		input      plugin.Plugin
		wantPlugin plugin.Plugin
		wantErr    string
	}{
		{
			name:       "listed plugin uses init source",
			initURL:    server.URL + "/plugins.yaml",
			input:      plugin.Plugin{Name: "itsm"},
			wantPlugin: listedItsm,
		},
		{
			name:    "flags override listed binary path and description",
			initURL: server.URL + "/plugins.yaml",
			input:   plugin.Plugin{Name: "itsm", BinaryPath: "/opt/bin/itsm", Description: "Custom"},
			wantPlugin: plugin.Plugin{
				Name:        "itsm",
				Description: "Custom",
				Script:      "https://example.com/itsm/install.sh",
				BinaryPath:  "/opt/bin/itsm",
			},
		},
		{
			name:       "unlisted plugin keeps default convention",
			initURL:    server.URL + "/plugins.yaml",
			input:      plugin.Plugin{Name: "other"},
			wantPlugin: plugin.Plugin{Name: "other"},
		},
		{
			name:       "no init URL keeps default convention",
			initURL:    "",
			input:      plugin.Plugin{Name: "itsm"},
			wantPlugin: plugin.Plugin{Name: "itsm"},
		},
		{
			name:    "unreachable init source fails",
			initURL: server.URL + "/missing.yaml",
			input:   plugin.Plugin{Name: "itsm"},
			wantErr: "HTTP 404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pluginFromInitSource(tt.initURL, tt.input)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantPlugin, got)
		})
	}
}
