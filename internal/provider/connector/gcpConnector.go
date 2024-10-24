package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"BucketName
	"os"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/hashicorp/terraform-plugin-log/tflog"
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
	return GcpConnectorGeneric{BucketName, FullFilePath, -1}
}

func NewNetwork(BucketName string, baseCidr string) GcpConnectorNetwork {
	fileName := fmt.Sprintf("cidr-reservation/baseCidr-%s.json", strings.Replace(strings.Replace(baseCidr, ".", "-", -1), "/", "-", -1))
	return GcpConnectorNetwork{BucketName, fileName, -1, baseCidr}
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

func (gcp *GcpConnectorGeneric) ReadRemote(ctx context.Context) (*NetworkConfig, error) {
	// Creates a client.
	networkConfig := NetworkConfig{}
	client, err := getStorageClient(ctx)
	if err != nil {
		return &networkConfig, err
	}
	defer client.Close()

	// Creates a Bucket instance.
	bucket := client.Bucket(gcp.BucketName)
	if err != nil {
		return nil, err
	}
	objectHandle := bucket.Object(gcp.FullFilePath)
	attrs, err := objectHandle.Attrs(ctx)
	if err == nil {
		gcp.Generation = attrs.Generation
	}
	rc, err := objectHandle.NewReader(ctx)
	if err != nil {
		return &networkConfig, err
	}
	defer rc.Close()
	slurp, err := io.ReadAll(rc)
	if err != nil {
		return &networkConfig, err
	}
	if err := json.Unmarshal(slurp, &networkConfig); err != nil {
		return &networkConfig, err
	}
	return &networkConfig, nil
}

func (gcp *GcpConnectorGeneric) WriteRemote(networkConfig *NetworkConfig, ctx context.Context) error {
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
	marshalled, err := json.Marshal(networkConfig)
	if err != nil {
		return err
	}
	_, _ = writer.Write(marshalled)
	if err := writer.Close(); err != nil {
		tflog.Error(ctx, "Failed to write file to GCP", map[string]interface{}{"error": err, "Generation": gcp.Generation})
		return err
	}
	return nil
}

func readNetsegmentJson(ctx context.Context, cidrProviderBucket string, netsegmentName string) (NetworkConfig, error) {
	return NetworkConfig{}, nil
	//return readRemote(cidrProviderBucket, fmt.Sprintf("gcp-cidr-provider/%s.json", netsegmentName), ctx)
}

// TODO: implement!
func uploadNewNetsegmentJson() {}
