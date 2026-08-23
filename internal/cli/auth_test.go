package cli

import "testing"

func TestProviderIDColumnWidthFitsAllProviders(t *testing.T) {
	longest := 0
	for _, provider := range allProviders {
		if len(provider.id) > longest {
			longest = len(provider.id)
		}
	}
	if providerIDColumnWidth != longest {
		t.Fatalf("provider ID column width = %d, want %d", providerIDColumnWidth, longest)
	}
}
