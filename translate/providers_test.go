package translate

import "testing"

func TestDefaultProvidersCanonicalizesLegacyCopilotID(t *testing.T) {
	providers := DefaultProviders()

	canonical := providers[ProviderGitHubCopilot]
	if canonical.ID != ProviderGitHubCopilot {
		t.Fatalf("canonical provider ID = %q, want %q", canonical.ID, ProviderGitHubCopilot)
	}
	legacy := providers["copilot"]
	if legacy.ID != ProviderGitHubCopilot {
		t.Fatalf("legacy provider ID = %q, want %q", legacy.ID, ProviderGitHubCopilot)
	}
}
