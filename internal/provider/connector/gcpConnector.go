package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/public-cloud-wl/tools/utils"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

type GcpConnectorGeneric struct {
	BucketName   string
	FullFilePath string
	Generation   int64
}

type GcpConnectorNetwork struct {
	GcpConnectorGeneric
	BaseCidrRange string
}

type NetworkConfig struct {
	Subnets map[string]string `json:"subnets"`
}

func NewGeneric(BucketName string, FullFilePath string) GcpConnectorGeneric {
	c := GcpConnectorGeneric{BucketName, FullFilePath, -1}

	return c
}

func NewNetwork(bucketName string, baseCidr string) GcpConnectorNetwork {
	fileName := fmt.Sprintf("gcsreferential/cidr-reservation/baseCidr-%s.json", strings.Replace(strings.Replace(baseCidr, ".", "-", -1), "/", "-", -1))
	return GcpConnectorNetwork{GcpConnectorGeneric{bucketName, fileName, -1}, baseCidr}
}

func getStorageClient(ctx context.Context) (*storage.Client, error) {
	access_token := os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN")
	if access_token != "" {
		var tokenSource oauth2.TokenSource
		var credOptions []option.ClientOption
		tokenSource = oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: access_token,
		})
		credOptions = append(credOptions, option.WithTokenSource(tokenSource))
		return storage.NewClient(ctx, credOptions...)
	} else {
		return storage.NewClient(ctx)
	}
}

func (gcp *GcpConnectorGeneric) Read(ctx context.Context, data interface{}) error {
	client, err := getStorageClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	bucket := client.Bucket(gcp.BucketName)
	if err != nil {
		return err
	}
	objectHandle := bucket.Object(gcp.FullFilePath)
	attrs, err := objectHandle.Attrs(ctx)
	if err == nil {
		gcp.Generation = attrs.Generation
	}
	rc, err := objectHandle.NewReader(ctx)
	if err != nil {
		tflog.Debug(ctx, fmt.Sprintf("Bucket Object does not exist with error : %s (%s)", gcp.FullFilePath, err.Error()))
		return err
	}
	defer rc.Close()
	slurp, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	err = json.Unmarshal(slurp, &data)
	if err != nil {
		return err
	}
	tflog.Debug(ctx, fmt.Sprintf("THIS IS CURRENTLY READ : %s", string(slurp)))
	return nil
}

func (gcp *GcpConnectorGeneric) Write(ctx context.Context, data interface{}) error {
	// Creates a client.
	client, err := getStorageClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	// Creates a Bucket instance.
	bucket := client.Bucket(gcp.BucketName)
	var writer *storage.Writer
	if gcp.Generation == -1 {
		writer = bucket.Object(gcp.FullFilePath).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	} else {
		writer = bucket.Object(gcp.FullFilePath).If(storage.Conditions{GenerationMatch: gcp.Generation}).NewWriter(ctx)
	}
	marshalled, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = writer.Write(marshalled)
	if err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		tflog.Error(ctx, "Failed to write file to GCP", map[string]interface{}{"error": err, "Generation": gcp.Generation, "Bucket": gcp.BucketName, "FilePath": gcp.FullFilePath})
		return err
	}
	// After successful close, update generation from the writer's attributes
	gcp.Generation = writer.Attrs().Generation
	tflog.Debug(ctx, fmt.Sprintf("THIS IS CURRENTLY WRITE : %s", string(marshalled)))
	return nil
}

func (gcp *GcpConnectorGeneric) GetAttrs(ctx context.Context) (*storage.ObjectAttrs, error) {
	client, err := getStorageClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	bucket := client.Bucket(gcp.BucketName)
	objectHandle := bucket.Object(gcp.FullFilePath)

	return objectHandle.Attrs(ctx)
}

func (gcp *GcpConnectorGeneric) Delete(ctx context.Context) error {
	// Creates a client.
	client, err := getStorageClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	// Creates a Bucket instance.
	bucket := client.Bucket(gcp.BucketName)
	return bucket.Object(gcp.FullFilePath).Delete(ctx)
}

func (gcp *GcpConnectorGeneric) GetLockPath(ctx context.Context) string {
	return fmt.Sprintf("%s.lock", gcp.FullFilePath)
}

func (gcp *GcpConnectorGeneric) Lock(ctx context.Context) (uuid.UUID, error) {
	tflog.Debug(ctx, "ENTERING TO LOCK")
	client, err := getStorageClient(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer client.Close()
	bucket := client.Bucket(gcp.BucketName)
	var writer *storage.Writer
	lockPath := gcp.GetLockPath(ctx)
	writer = bucket.Object(lockPath).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	if writer == nil {
		return uuid.Nil, errors.New("Condition not met")
	}
	lockId := uuid.New()
	_, err = writer.Write([]byte(lockId.String()))
	if err != nil {
		tflog.Debug(ctx, fmt.Sprintf("CANNOT GET LOCK : %s", err.Error()))
		return uuid.Nil, err
	}
	if err := writer.Close(); err != nil {
		tflog.Debug(ctx, fmt.Sprintf("CANNOT GET LOCK : %s", err.Error()))
		return uuid.Nil, err
	}
	tflog.Debug(ctx, fmt.Sprintf("LOCK GENERATED : %s", lockId))
	return lockId, nil

}

func (gcp *GcpConnectorGeneric) Unlock(ctx context.Context, lockId uuid.UUID) error {
	var err error
	tflog.Debug(ctx, fmt.Sprintf("ENTERING TO UNLOCK : %s", lockId.String()))
	client, err := getStorageClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	lockPath := gcp.GetLockPath(ctx)
	bucket := client.Bucket(gcp.BucketName)
	objectHandle := bucket.Object(lockPath)
	_, err = objectHandle.Attrs(ctx)
	if err != nil {
		return err
	}
	rc, err := objectHandle.NewReader(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	slurp, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	currentLockId := string(slurp)
	tflog.Debug(ctx, fmt.Sprintf("COMPARING LOCKID : %s and %s", lockId.String(), currentLockId))
	if currentLockId == lockId.String() {
		tflog.Debug(ctx, fmt.Sprintf("UNLOCKING LOCKID : %s", currentLockId))
		// retry.
		return utils.Retry(func() error { return bucket.Object(lockPath).Delete(ctx) }, 5)
	} else {
		tflog.Debug(ctx, fmt.Sprintf("LOCKID DOES NOT CORRESPOND: %s %s", currentLockId, lockId.String()))
		return errors.New("The lock id does not correspond, cannot unlock it")
	}
}

// Get the current lock ID if there is one at string format and send error if there is no lock, error will be nil if there is a lock that can be retrieve.
func (gcp *GcpConnectorGeneric) GetCurrentLockId(ctx context.Context) (uuid.UUID, error) {
	var err error
	client, err := getStorageClient(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer client.Close()
	lockPath := gcp.GetLockPath(ctx)
	bucket := client.Bucket(gcp.BucketName)
	if err != nil {
		return uuid.Nil, err
	}
	objectHandle := bucket.Object(lockPath)
	rc, err := objectHandle.NewReader(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer rc.Close()
	slurp, err := io.ReadAll(rc)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.MustParse(string(slurp)), nil
}

// Wait for lock to be relase and create a new one.
func (gcp *GcpConnectorGeneric) WaitForlock(ctx context.Context, timeout time.Duration, backoffMultiplier float32, existingLock ...uuid.UUID) (uuid.UUID, error) {
	startTime := time.Now()
	numberOfIteration := 0
	var err error
	var lock uuid.UUID
	const minBackoff = 1 * time.Second
	const maxBackoff = 10 * time.Second
	// Infinite loop break by return.
	for {
		if time.Since(startTime) > timeout {
			tflog.Info(ctx, "CANNOT WAIT MORE FOR LOCK ")
			return uuid.Nil, fmt.Errorf("CANNOT WAIT MORE FOR LOCK")
		}
		lock, err = gcp.GetCurrentLockId(ctx)
		if err == nil {
			tflog.Debug(ctx, fmt.Sprintf("CURRENT LOCKID : %s", lock.String()))
			if len(existingLock) > 0 && lock == existingLock[0] {
				tflog.Debug(ctx, fmt.Sprintf("COMPARED LOCKID ARE SAME: %s", existingLock[0].String()))
				return lock, nil
			}
			tflog.Debug(ctx, fmt.Sprintf("LOCK WAS REQUEST BY ANOTHER PROCESS : %s", lock.String()))
		} else {
			// There is no lock so try to get one.
			tflog.Debug(ctx, "No lock detected so attempt to get one")
			lock, err = gcp.Lock(ctx)
			if err == nil {
				tflog.Debug(ctx, fmt.Sprintf("LOCK RETRIEVED %s", lock.String()))
				return lock, nil
			}
			tflog.Debug(ctx, "THERE IS ERROR CREATING NEW LOCK, WAIT AGAIN")
		}
		// Backoff sleep.
		numberOfIteration++
		baseBackoff := time.Duration(numberOfIteration) * minBackoff
		if baseBackoff > maxBackoff {
			baseBackoff = maxBackoff
		}

		jitter := time.Duration(rand.Int63n(int64(baseBackoff / 2)))
		sleepTime := baseBackoff - (baseBackoff / 4) + jitter

		// Do not sleep more than remaining time.
		remainingTime := timeout - time.Since(startTime)
		if sleepTime > remainingTime {
			sleepTime = remainingTime
		}
		if sleepTime <= 0 {
			return uuid.Nil, fmt.Errorf("TIMEOUT waiting for lock")
		}
		tflog.Debug(ctx, fmt.Sprintf("WAIT %s before new lock try (iteration %d)", sleepTime, numberOfIteration))

		select {
		case <-time.After(sleepTime):
			time.Sleep(sleepTime)
		case <-ctx.Done():
			return uuid.Nil, fmt.Errorf("Context canceled while waiting for lock: %w", ctx.Err())
		}
	}
}
