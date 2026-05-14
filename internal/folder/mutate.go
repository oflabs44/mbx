package folder

import "context"

// Adder, Deleter, Expunger, Purger are the narrow consumer interfaces
// for the four folder write-path verbs. Defined here, next to the cmd
// handlers that depend on them; backends in internal/provider/* satisfy
// them by writing methods with matching signatures.
//
// Capability asymmetry: only IMAP can implement Expunger meaningfully
// (Gmail has no manual expunge equivalent — Trash is auto-purged after
// 30 days). Gmail-side Expunger returns nil as a documented no-op so
// the verb stays safe to run unconditionally in cross-backend scripts.
type Adder interface {
	AddFolder(ctx context.Context, name string) error
}

type Deleter interface {
	DeleteFolder(ctx context.Context, name string, force bool) error
}

type Expunger interface {
	ExpungeFolder(ctx context.Context, name string) error
}

type Purger interface {
	PurgeFolder(ctx context.Context, name string) error
}
