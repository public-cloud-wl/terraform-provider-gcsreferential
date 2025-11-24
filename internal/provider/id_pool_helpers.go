package provider

import (
	"context"
	"errors"

	"cloud.google.com/go/storage"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	IdPoolTools "github.com/public-cloud-wl/tools/idPoolTools"
	"github.com/terraform-provider-gcsreferential/internal/provider/connector"
)

// getAndCacheIdPool retrieves an ID pool, utilizing a cache to minimize GCS reads.
// It checks the remote object's generation against the cached version. If they differ,
// it fetches the latest version from GCS and updates the cache.
// This function assumes that a higher-level lock (e.g., a GCS lock file) is already held
// to prevent race conditions between different Terraform processes.
func getAndCacheIdPool(ctx context.Context, p *GCSReferentialProviderModel, poolName string, gcpConnector *connector.GcpConnectorGeneric) (*CachedIdPool, error) {
	p.CacheMutex.Lock()
	defer p.CacheMutex.Unlock()

	// Get remote object attributes to check generation.
	attrs, err := gcpConnector.GetAttrs(ctx)
	if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		return nil, err
	}

	remoteGeneration := int64(-1)
	if err == nil {
		remoteGeneration = attrs.Generation
	}

	// Always update the connector's generation to what was just observed from the remote state.
	gcpConnector.Generation = remoteGeneration

	// Check if a valid, up-to-date pool is already in the cache.
	if cachedPool, ok := p.IdPoolsCache[poolName]; ok && cachedPool.Generation == remoteGeneration {
		tflog.Debug(ctx, "Cache hit for pool", map[string]interface{}{"pool": poolName, "generation": remoteGeneration})
		return cachedPool, nil
	}

	// Cache miss or stale data: read from GCS.
	tflog.Debug(ctx, "Cache miss for pool", map[string]interface{}{"pool": poolName})
	var pool IdPoolTools.IDPool
	err = gcpConnector.Read(ctx, &pool)
	if err != nil {
		// If the object doesn't exist, remove it from cache in case it's a stale entry.
		if errors.Is(err, storage.ErrObjectNotExist) {
			delete(p.IdPoolsCache, poolName)
		}
		return nil, err
	}

	// Reconcile the pool's internal state after reading from JSON.
	members := pool.Members
	reconciledPoolPtr := IdPoolTools.NewIDPool(pool.StartFrom, pool.EndTo)
	for _, allocatedID := range members {
		reconciledPoolPtr.Remove(allocatedID)
	}
	reconciledPoolPtr.Members = members

	// Store the newly read and reconciled pool in the cache.
	newCachedPool := &CachedIdPool{
		Pool:       reconciledPoolPtr,
		Generation: gcpConnector.Generation, // Read() updates the connector's generation.
	}
	p.IdPoolsCache[poolName] = newCachedPool
	tflog.Debug(ctx, "Cached new pool version", map[string]interface{}{"pool": poolName, "generation": newCachedPool.Generation})

	return newCachedPool, nil
}
