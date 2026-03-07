// CLAUDE:SUMMARY Sentinel errors for pool, shard, and factory failure conditions.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS ErrShardNotFound, ErrShardArchived, ErrNoFactory, ErrPoolClosed
package tenant

import "errors"

var (
	// ErrShardNotFound is returned when the requested shard does not exist in the catalog.
	ErrShardNotFound = errors.New("tenant: shard not found in catalog")

	// ErrShardArchived is returned when the requested shard has been archived.
	ErrShardArchived = errors.New("tenant: shard is archived")

	// ErrShardDeleted is returned when the requested shard has been deleted.
	ErrShardDeleted = errors.New("tenant: shard is deleted")

	// ErrShardUnavailable is returned by the noop factory for disabled shards.
	ErrShardUnavailable = errors.New("tenant: shard is unavailable")

	// ErrPoolExhausted is returned when maxOpen connections are reached and no idle
	// connection can be evicted.
	ErrPoolExhausted = errors.New("tenant: max open connections reached")

	// ErrFactoryNotFound is returned when no factory is registered for the shard's strategy.
	ErrFactoryNotFound = errors.New("tenant: no factory for strategy")

	// ErrFactoryFailed is returned when a factory returns an error while creating
	// a database connection.
	ErrFactoryFailed = errors.New("tenant: factory returned error")

	// ErrPoolClosed is returned when operations are attempted on a closed pool.
	ErrPoolClosed = errors.New("tenant: pool is closed")

	// ErrOwnershipMismatch is returned when the caller's ownerID does not match
	// the shard's recorded owner_id in the catalog snapshot.
	ErrOwnershipMismatch = errors.New("tenant: ownership mismatch")

	// Legacy aliases — kept so existing imports don't break during migration.
	ErrSpaceNotFound    = ErrShardNotFound
	ErrSpaceArchived    = ErrShardArchived
	ErrSpaceDeleted     = ErrShardDeleted
	ErrSpaceUnavailable = ErrShardUnavailable
)
