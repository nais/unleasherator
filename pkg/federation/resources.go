package federation

import (
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/pb"
)

func UnleashFederationInstance(unleash *unleashv1.Unleash, token string) *pb.Instance {
	return &pb.Instance{
		Version:     pb.Version,
		Status:      pb.Status_Provisioned,
		Name:        unleash.GetName(),
		Url:         unleash.PublicSecureURL(),
		SecretToken: token,
		Namespaces:  []string{unleash.GetName()},
	}
}
