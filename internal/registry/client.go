// internal/registry/client.go
package registry

import (
	"context"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// RegistryClient kapselt die reinen I/O-Aufrufe an die Container Registry.
type RegistryClient interface {
	ListTags(ctx context.Context, repo string) ([]string, error)
	GetDigest(ctx context.Context, ref string) (string, error)
}

// RemoteClient ist die direkte, ungecachte Implementierung.
type RemoteClient struct{}

func NewRemoteClient() *RemoteClient {
	return &RemoteClient{}
}

func (c *RemoteClient) ListTags(ctx context.Context, repoName string) ([]string, error) {
	repo, err := name.NewRepository(repoName)
	if err != nil {
		return nil, err
	}
	return remote.List(repo, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
}

func (c *RemoteClient) GetDigest(ctx context.Context, refString string) (string, error) {
	ref, err := name.ParseReference(refString)
	if err != nil {
		return "", err
	}
	desc, err := remote.Head(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}
