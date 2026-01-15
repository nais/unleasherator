package federation

import (
	"hash/fnv"
	"sort"
	"strings"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/pb"
)

func UnleashFederationInstance(unleash *unleashv1.Unleash, token string) *pb.Instance {
	return unleashFederationInstanceWithStatus(unleash, token, pb.Status_Provisioned)
}

func UnleashFederationInstanceRemoved(unleash *unleashv1.Unleash) *pb.Instance {
	return unleashFederationInstanceWithStatus(unleash, "", pb.Status_Removed)
}

func unleashFederationInstanceWithStatus(unleash *unleashv1.Unleash, token string, status pb.Status) *pb.Instance {
	return &pb.Instance{
		Version:     pb.Version,
		Status:      status,
		Name:        unleash.GetName(),
		Url:         unleash.PublicApiURL(),
		SecretToken: token,
		SecretNonce: unleash.Spec.Federation.SecretNonce,
		Namespaces:  unleash.Spec.Federation.Namespaces,
		Clusters:    unleash.Spec.Federation.Clusters,
	}
}

// ComputeInstanceHash computes a FNV-1a hash of the federation instance data.
// This is used to detect changes and avoid redundant publishes.
// The hash includes all fields that affect the published message.
// Returns int64 for Kubernetes CRD compatibility (OpenAPI 3.0 doesn't support uint64).
func ComputeInstanceHash(instance *pb.Instance) int64 {
	h := fnv.New64a()

	// Write deterministic representation of instance fields
	h.Write([]byte(instance.Name))
	h.Write([]byte{0}) // separator
	h.Write([]byte(instance.Url))
	h.Write([]byte{0})
	h.Write([]byte(instance.SecretToken))
	h.Write([]byte{0})
	h.Write([]byte(instance.SecretNonce))
	h.Write([]byte{0})

	// Sort slices for deterministic hashing
	namespaces := make([]string, len(instance.Namespaces))
	copy(namespaces, instance.Namespaces)
	sort.Strings(namespaces)
	h.Write([]byte(strings.Join(namespaces, ",")))
	h.Write([]byte{0})

	clusters := make([]string, len(instance.Clusters))
	copy(clusters, instance.Clusters)
	sort.Strings(clusters)
	h.Write([]byte(strings.Join(clusters, ",")))

	// Cast to int64 for Kubernetes CRD compatibility
	return int64(h.Sum64())
}
