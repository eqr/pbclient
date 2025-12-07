package migrations

import "github.com/eqr/pbclient"

// Migration defines a reversible change applied through PocketBase using pbclient.
// Implementations must return a stable, sortable name (e.g. 20250121_add_users)
// and provide Up/Down hooks that perform the forward and rollback work.
type Migration interface {
	Name() string
	Up(client pbclient.AuthenticatedClient) error
	Down(client pbclient.AuthenticatedClient) error
}
