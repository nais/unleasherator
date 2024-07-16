package federation

import (
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/pb"
)

func UnleashFederationInstance(unleash *unleashv1.Unleash, token string) *pb.Instance {
	return &pb.Instance{
		Version:     pb.Version,
		Status:      pb.Status_Provisioned,
		Name:        unleash.GetName(),
		Url:         unleash.PublicApiURL(),
		SecretToken: token,
		SecretNonce: unleash.Spec.Federation.SecretNonce,
		Namespaces:  unleash.Spec.Federation.Namespaces,
		Clusters:    unleash.Spec.Federation.Clusters,
	}
}
