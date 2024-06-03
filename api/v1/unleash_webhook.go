/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package unleash_nais_io_v1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var unleashlog = logf.Log.WithName("unleash-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (u *Unleash) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(u).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-unleash-nais-io-v1-unleash,mutating=true,failurePolicy=fail,sideEffects=None,groups=unleash.nais.io,resources=unleashes,verbs=create;update,versions=v1,name=munleash.kb.io,admissionReviewVersions=v1

// TODO: Defaulter is deprecated, use webhook.CustomDefaulter instead
var _ webhook.Defaulter = &Unleash{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (u *Unleash) Default() {
	unleashlog.Info("default", "name", u.Name)

	if u.Spec.Database.SecretName == "" {
		u.Spec.Database.SecretName = "my-secret"
	}
	if u.Spec.Database.SecretUserKey == "" {
		u.Spec.Database.SecretUserKey = "POSTGRES_USER"
	}
	if u.Spec.Database.SecretPassKey == "" {
		u.Spec.Database.SecretPassKey = "POSTGRES_PASSWORD"
	}
	if u.Spec.Database.SecretDatabaseNameKey == "" {
		u.Spec.Database.SecretDatabaseNameKey = "POSTGRES_DB"
	}
	if u.Spec.Database.Host == "" {
		u.Spec.Database.Host = "localhost"
	}
	if u.Spec.Database.Port == "" {
		u.Spec.Database.Port = "5432"
	}
	if u.Spec.Database.SSL == "" {
		u.Spec.Database.SSL = "false"
	}

	if u.Spec.WebIngress.Enabled == nil {
		u.Spec.WebIngress.Enabled = true
	}
	if u.Spec.WebIngress.Class == "" {
		u.Spec.WebIngress.Class = "nginx"
	}
	if u.Spec.WebIngress.Path == "" {
		u.Spec.WebIngress.Path = "/"
	}

	if u.Spec.ApiIngress.Enabled == nil {
		u.Spec.ApiIngress.Enabled = true
	}
	if u.Spec.ApiIngress.Class == "" {
		u.Spec.ApiIngress.Class = "nginx"
	}
	if u.Spec.ApiIngress.Path == "" {
		u.Spec.ApiIngress.Path = "/"
	}

	// TODO move to config
	defaultEnvVars := []corev1.EnvVar{{
		Name:  "GOOGLE_IAP_AUDIENCE",
		Value: googleIapAudience,
	}}

	if u.Spec.ExtraEnvVars == nil {
		u.Spec.ExtraEnvVars = []corev1.EnvVar{}
	}

	for _, envVar := range defaultEnvVars {
		if !containsEnvVar(u.Spec.ExtraEnvVars, envVar.Name) {
			u.Spec.ExtraEnvVars = append(u.Spec.ExtraEnvVars, envVar)
		}
	}

	if





}

func unleashSpec(team slug.Slug) *unleash_nais_io_v1.Unleash {
	cloudSqlProto := corev1.ProtocolTCP
	cloudSqlPort := intstr.FromInt(3307)







		Spec: unleash_nais_io_v1.UnleashSpec{
			NetworkPolicy: unleash_nais_io_v1.UnleashNetworkPolicyConfig{
				Enabled:  true,
				AllowDNS: true,
				ExtraEgressRules: []networkingv1.NetworkPolicyEgressRule{
					{
						Ports: []networkingv1.NetworkPolicyPort{{
							Protocol: &cloudSqlProto,
							Port:     &cloudSqlPort,
						}},
						To: []networkingv1.NetworkPolicyPeer{{
							IPBlock: &networkingv1.IPBlock{
								CIDR: sqlCidr,
							},
						}},
					},
				},
			},
			ExtraEnvVars: []corev1.EnvVar{{
				Name:  "GOOGLE_IAP_AUDIENCE",
				Value: googleIapAudience,
			}, {
				Name:  "TEAMS_API_URL",
				Value: teamsApiUrl,
			}, {
				Name: "TEAMS_API_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: teamsApiSecretName,
						},
						Key: teamsApiSecretKey,
					},
				},
			}, {
				Name:  "TEAMS_ALLOWED_TEAMS",
				Value: name,
			}, {
				Name:  "LOG_LEVEL",
				Value: "info",
			}, {
				Name:  "DATABASE_POOL_MAX",
				Value: "3",
			}, {
				Name:  "DATABASE_POOL_IDLE_TIMEOUT_MS",
				Value: "100",
			}},
			ExtraContainers: []corev1.Container{{
				Name:  "sql-proxy",
				Image: SqlProxyImage,
				Args: []string{
					"--structured-logs",
					"--port=5432",
					fmt.Sprintf("%s:%s:%s", googleProjectId,
						sqlRegion,
						sqlId),
				},
				SecurityContext: &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
					Privileged:               boolRef(false),
					RunAsUser:                int64Ref(65532),
					RunAsNonRoot:             boolRef(true),
					AllowPrivilegeEscalation: boolRef(false),
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(SqlProxyRequestCPU),
						corev1.ResourceMemory: resource.MustParse(SqlProxyRequestMemory),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(SqlProxyLimitMemory),
					},
				},
			}},
			ExistingServiceAccountName: sqlUserSericeAccount,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(UnleashRequestCPU),
					corev1.ResourceMemory: resource.MustParse(UnleashRequestMemory),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse(UnleashLimitMemory),
				},
			},
		},
	}
}
