package resources

import (
	"fmt"
	"os"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/utils"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Defaults for the Unleash custom resource
const (
	// DefaultUnleashImageRegistry is the default registry for the Unleash image
	DefaultUnleashImageRegistry = "europe-north1-docker.pkg.dev/nais-io/nais/images"
	// DefaultUnleashImageName is the default image name used for the Unleash deployment
	DefaultUnleashImageName = "unleash-v4"
	// DefaultUnleashVersion is the default version used for the Unleash deployment
	DefaultUnleashVersion = "v4.23.1"
	// DefaultUnleashPort is the default port used for the Unleash deployment
	DefaultUnleashPort = 4242
	// DefaultUnleashPortName is the default port name used for the Unleash deployment
	DefaultUnleashPortName = "http"
)

const (
	EnvInitAdminAPIToken = "INIT_ADMIN_API_TOKENS"
	EnvDatabaseURL       = "DATABASE_URL"
	EnvDatabaseUser      = "DATABASE_USER"
	EnvDatabasePass      = "DATABASE_PASS"
	EnvDatabaseName      = "DATABASE_NAME"
	EnvDatabaseHost      = "DATABASE_HOST"
	EnvDatabasePort      = "DATABASE_PORT"
	EnvDatabaseSSL       = "DATABASE_SSL"
)

func GenerateAdminKey() (string, error) {
	random, err := utils.RandomString(32)
	return fmt.Sprintf("*:*.%s", random), err
}

func ServiceMonitorForUnleash(unleash *unleashv1.Unleash, scheme *runtime.Scheme) (*monitoringv1.ServiceMonitor, error) {
	ls := labelsForUnleash(unleash.GetName())

	serviceMonitor := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.Name,
			Namespace: unleash.Namespace,
			Labels:    ls,
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Endpoints: []monitoringv1.Endpoint{
				{
					Port: DefaultUnleashPortName,
					Path: "/internal-backstage/prometheus",
				},
			},
			Selector: metav1.LabelSelector{
				MatchLabels: ls,
			},
		},
	}

	if err := ctrl.SetControllerReference(unleash, serviceMonitor, scheme); err != nil {
		return nil, err
	}

	return serviceMonitor, nil
}

// Creates a secret that is managed by way of controller reference
func InstanceSecretForUnleash(unleash *unleashv1.Unleash, scheme *runtime.Scheme, adminKey string) (*corev1.Secret, error) {
	ls := labelsForUnleash(unleash.GetName())

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.GetInstanceSecretName(),
			Namespace: unleash.Namespace,
			Labels:    ls,
		},
		StringData: map[string]string{
			unleashv1.UnleashSecretTokenKey: adminKey,
		},
	}

	if err := ctrl.SetControllerReference(unleash, secret, scheme); err != nil {
		return nil, err
	}

	return secret, nil
}

// Creates a secret that is not managed, i.e. without a controllerReference
func OperatorSecretForUnleash(name, secretName, namespace, adminKey string) *corev1.Secret {
	ls := labelsForUnleash(name)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels:    ls,
		},
		StringData: map[string]string{
			unleashv1.UnleashSecretTokenKey: adminKey,
		},
	}

	return secret
}

func ServiceAccountForUnleash(unleash *unleashv1.Unleash, scheme *runtime.Scheme) (*corev1.ServiceAccount, error) {
	ls := labelsForUnleash(unleash.GetName())

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.Name,
			Namespace: unleash.Namespace,
			Labels:    ls,
		},
	}

	if err := ctrl.SetControllerReference(unleash, sa, scheme); err != nil {
		return nil, err
	}

	return sa, nil
}

func ServiceForUnleash(unleash *unleashv1.Unleash, scheme *runtime.Scheme) (*corev1.Service, error) {
	ls := labelsForUnleash(unleash.GetName())

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.Name,
			Namespace: unleash.Namespace,
			Labels:    ls,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       DefaultUnleashPortName,
					Port:       80,
					TargetPort: intstr.FromInt(DefaultUnleashPort),
				},
			},
			Selector: ls,
		},
	}

	if err := ctrl.SetControllerReference(unleash, svc, scheme); err != nil {
		return nil, err
	}

	return svc, nil
}

func DeploymentForUnleash(unleash *unleashv1.Unleash, scheme *runtime.Scheme) (*appsv1.Deployment, error) {
	ls := labelsForUnleash(unleash.GetName())
	replicas := unleash.Spec.Size

	envVars, err := envVarsForUnleash(unleash)
	if err != nil {
		return nil, err
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.Name,
			Namespace: unleash.Namespace,
			Labels:    ls,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					//Affinity: &corev1.Affinity{
					//	NodeAffinity: &corev1.NodeAffinity{
					//		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				{
					//					MatchExpressions: []corev1.NodeSelectorRequirement{
					//						{
					//							Key:      "kubernetes.io/arch",
					//							Operator: "In",
					//							Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						},
					//						{
					//							Key:      "kubernetes.io/os",
					//							Operator: "In",
					//							Values:   []string{"linux"},
					//						},
					//					},
					//				},
					//			},
					//		},
					//	},
					//},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/health",
									Port: intstr.FromInt(DefaultUnleashPort),
								},
							},
							InitialDelaySeconds: 5,
							TimeoutSeconds:      10,
							PeriodSeconds:       10,
						},
						Image:           ImageForUnleash(unleash),
						Name:            "unleash",
						ImagePullPolicy: corev1.PullAlways,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &corev1.SecurityContext{
							// WARNING: Ensure that the image used defines an UserID in the Dockerfile
							// otherwise the Pod will not run and will fail with "container has runAsNonRoot and image has non-numeric user"".
							// If you want your workloads admitted in namespaces enforced with the restricted mode in OpenShift/OKD vendors
							// then, you MUST ensure that the Dockerfile defines a User ID OR you MUST leave the "RunAsNonRoot" and
							// "RunAsUser" fields empty.
							RunAsNonRoot: &[]bool{true}[0],
							// The unleash image does not use a non-zero numeric user as the default user.
							// Due to RunAsNonRoot field being set to true, we need to force the user in the
							// container to a non-zero numeric user. We do this using the RunAsUser field.
							// However, if you are looking to provide solution for K8s vendors like OpenShift
							// be aware that you cannot run under its restricted-v2 SCC if you set this value.
							RunAsUser:                &[]int64{1001}[0],
							AllowPrivilegeEscalation: &[]bool{false}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: DefaultUnleashPort,
							Name:          DefaultUnleashPortName,
						}},
						// Command: []string{"unleash", "-m=64", "-o", "modern", "-v"},
						// Secret environment variables
						Env:       envVars,
						Resources: unleash.Spec.Resources,
					}},
				},
			},
		},
	}
	if unleash.Spec.ExistingServiceAccountName != "" {
		dep.Spec.Template.Spec.ServiceAccountName = unleash.Spec.ExistingServiceAccountName
	}
	if unleash.Spec.ExtraContainers != nil {
		dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, unleash.Spec.ExtraContainers...)
	}
	if unleash.Spec.ExtraVolumes != nil {
		dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes, unleash.Spec.ExtraVolumes...)
	}
	if unleash.Spec.ExtraVolumeMounts != nil {
		dep.Spec.Template.Spec.Containers[0].VolumeMounts = append(dep.Spec.Template.Spec.Containers[0].VolumeMounts, unleash.Spec.ExtraVolumeMounts...)
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(unleash, dep, scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// labelsForUnleash returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForUnleash(instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "Unleash",
		"app.kubernetes.io/instance":   instanceName,
		"app.kubernetes.io/part-of":    "unleasherator",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}

// IngressForUnleash returns the Ingress for Unleash Deployment
func IngressForUnleash(unleash *unleashv1.Unleash, config *unleashv1.UnleashIngressConfig, nameSuffix string, scheme *runtime.Scheme) (*networkingv1.Ingress, error) {
	labels := labelsForUnleash(unleash.GetName())
	pathType := networkingv1.PathTypeImplementationSpecific
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.NamespacedNameWithSuffix(nameSuffix).Name,
			Namespace: unleash.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &config.Class,
			Rules: []networkingv1.IngressRule{
				{
					Host: config.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     config.Path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: unleash.Name,
											Port: networkingv1.ServiceBackendPort{
												Name: DefaultUnleashPortName,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(unleash, ingress, scheme); err != nil {
		return nil, err
	}
	return ingress, nil
}

// NetworkPolicyForUnleash returns the NetworkPolicy for the Unleash Deployment
func NetworkPolicyForUnleash(unleash *unleashv1.Unleash, scheme *runtime.Scheme, operatorNamespace string) (*networkingv1.NetworkPolicy, error) {
	labels := labelsForUnleash(unleash.GetName())

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleash.Name,
			Namespace: unleash.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: labels,
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						// Allow traffic from unleasherator namespace for API calls
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": operatorNamespace,
								},
							},
						},
					},
				},
			},
		},
	}

	if unleash.Spec.NetworkPolicy.AllowDNS {
		np.Spec.Egress = append(np.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						//MatchLabels: map[string]string{
						//	"kubernetes.io/metadata.name": "kube-system",
						//},
					},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"k8s-app": "kube-dns",
						},
					},
				},
				{
					NamespaceSelector: &metav1.LabelSelector{},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"k8s-app": "node-local-dns",
						},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &[]corev1.Protocol{corev1.ProtocolUDP}[0],
					Port: &intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 53,
					},
				},
				{
					Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
					Port: &intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 53,
					},
				},
			},
		})
	}

	if unleash.Spec.NetworkPolicy.AllowAllFromSameNamespace {
		np.Spec.Ingress = append(np.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{},
				},
			},
		})
	}

	if unleash.Spec.NetworkPolicy.AllowAllFromCluster {
		np.Spec.Ingress = append(np.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{},
				},
			},
		})
	}

	if len(unleash.Spec.NetworkPolicy.AllowAllFromNamespaces) > 0 {
		for _, ns := range unleash.Spec.NetworkPolicy.AllowAllFromNamespaces {
			np.Spec.Ingress = append(np.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
				From: []networkingv1.NetworkPolicyPeer{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": ns,
							},
						},
					},
				},
			})
		}
	}

	np.Spec.Egress = append(np.Spec.Egress, unleash.Spec.NetworkPolicy.ExtraEgressRules...)
	np.Spec.Ingress = append(np.Spec.Ingress, unleash.Spec.NetworkPolicy.ExtraIngressRules...)

	// Set the ownerRef for the NetworkPolicy
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(unleash, np, scheme); err != nil {
		return nil, err
	}
	return np, nil
}

// ImageForUnleash gets the Operand image which is managed by this controller
// from the UNLEASH_IMAGE environment variable defined in the config/manager/manager.yaml
func ImageForUnleash(unleash *unleashv1.Unleash) string {
	if unleash.Spec.CustomImage != "" {
		return unleash.Spec.CustomImage
	}
	var imageEnvVar = "UNLEASH_IMAGE"
	image, found := os.LookupEnv(imageEnvVar)
	if !found {
		image = fmt.Sprintf("%s/%s:%s", DefaultUnleashImageRegistry, DefaultUnleashImageName, DefaultUnleashVersion)
	}
	return image
}

// envVarsForUnleash returns the environment variables for the Unleash Deployment
func envVarsForUnleash(unleash *unleashv1.Unleash) ([]corev1.EnvVar, error) {
	secretName := unleash.Spec.Database.SecretName
	secretURLKey := unleash.Spec.Database.SecretURLKey
	databaseURL := unleash.Spec.Database.URL

	envVars := []corev1.EnvVar{utils.SecretEnvVar(EnvInitAdminAPIToken, unleash.GetInstanceSecretName(), unleashv1.UnleashSecretTokenKey)}

	if databaseURL != "" {
		return append(envVars, corev1.EnvVar{
			Name:  EnvDatabaseURL,
			Value: databaseURL,
		}), nil
	}

	if secretName == "" {
		return envVars, &ValidationError{
			err:      fmt.Errorf("either database.url or database.secretName must be set"),
			resource: "Deployment",
		}
	}

	if secretURLKey != "" {
		return append(envVars, utils.SecretEnvVar(EnvDatabaseURL, secretName, secretURLKey)), nil
	}

	envVars = append(envVars, utils.SecretEnvVar(EnvDatabasePass, secretName, unleash.Spec.Database.SecretPassKey))

	if unleash.Spec.Database.SecretUserKey != "" {
		envVars = append(envVars, utils.SecretEnvVar(EnvDatabaseUser, secretName, unleash.Spec.Database.SecretUserKey))
	} else if unleash.Spec.Database.User != "" {
		envVars = append(envVars, utils.EnvVar(EnvDatabaseUser, unleash.Spec.Database.User))
	} else {
		return envVars, &ValidationError{
			err:      fmt.Errorf("either database.username or database.SecretUserKey must be set"),
			resource: "Deployment",
		}
	}

	if unleash.Spec.Database.SecretDatabaseNameKey != "" {
		envVars = append(envVars, utils.SecretEnvVar(EnvDatabaseName, secretName, unleash.Spec.Database.SecretDatabaseNameKey))
	} else if unleash.Spec.Database.DatabaseName != "" {
		envVars = append(envVars, utils.EnvVar(EnvDatabaseName, unleash.Spec.Database.DatabaseName))
	} else {
		return envVars, &ValidationError{
			err:      fmt.Errorf("either database.databaseName or database.secretDatabaseNameKey must be set"),
			resource: "Deployment",
		}
	}

	if unleash.Spec.Database.SecretHostKey != "" {
		envVars = append(envVars, utils.SecretEnvVar(EnvDatabaseHost, secretName, unleash.Spec.Database.SecretHostKey))
	} else if unleash.Spec.Database.Host != "" {
		envVars = append(envVars, utils.EnvVar(EnvDatabaseHost, unleash.Spec.Database.Host))
	} else {
		return envVars, &ValidationError{
			err:      fmt.Errorf("either database.host or database.secretHostKey must be set"),
			resource: "Deployment",
		}
	}

	if unleash.Spec.Database.SecretPortKey != "" {
		envVars = append(envVars, utils.SecretEnvVar(EnvDatabasePort, secretName, unleash.Spec.Database.SecretPortKey))
	} else if unleash.Spec.Database.Port != "" {
		envVars = append(envVars, utils.EnvVar(EnvDatabasePort, unleash.Spec.Database.Port))
	} else {
		return envVars, &ValidationError{
			err:      fmt.Errorf("either database.port or database.secretPortKey must be set"),
			resource: "Deployment",
		}
	}

	if unleash.Spec.Database.SecretSSLKey != "" {
		envVars = append(envVars, utils.SecretEnvVar(EnvDatabaseSSL, secretName, unleash.Spec.Database.SecretSSLKey))
	} else if unleash.Spec.Database.SSL != "" {
		envVars = append(envVars, utils.EnvVar(EnvDatabaseSSL, unleash.Spec.Database.SSL))
	}

	envVars = append(envVars, utils.EnvVar(EnvDatabaseURL, fmt.Sprintf(
		"postgres://$(%s):$(%s)@$(%s):$(%s)/$(%s)",
		EnvDatabaseUser,
		EnvDatabasePass,
		EnvDatabaseHost,
		EnvDatabasePort,
		EnvDatabaseName,
	)))

	if unleash.Spec.ExtraEnvVars != nil {
		envVars = append(envVars, unleash.Spec.ExtraEnvVars...)
	}

	return envVars, nil
}
