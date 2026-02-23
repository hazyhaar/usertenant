package tenant

import "errors"

var (
	// ErrSpaceNotFound is returned when the requested space does not exist in the catalog.
	ErrSpaceNotFound = errors.New("tenant: space not found in catalog")

	// ErrSpaceArchived is returned when the requested space has been archived.
	ErrSpaceArchived = errors.New("tenant: space is archived")

	// ErrSpaceDeleted is returned when the requested space has been deleted.
	ErrSpaceDeleted = errors.New("tenant: space is deleted")

	// ErrSpaceUnavailable is returned by the noop factory for disabled spaces.
	ErrSpaceUnavailable = errors.New("tenant: space is unavailable")

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
)
