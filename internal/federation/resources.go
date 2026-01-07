package federation

import (
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
