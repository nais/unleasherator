//go:build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package unleash_nais_io_v1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApiToken) DeepCopyInto(out *ApiToken) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApiToken.
func (in *ApiToken) DeepCopy() *ApiToken {
	if in == nil {
		return nil
	}
	out := new(ApiToken)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ApiToken) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApiTokenList) DeepCopyInto(out *ApiTokenList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ApiToken, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApiTokenList.
func (in *ApiTokenList) DeepCopy() *ApiTokenList {
	if in == nil {
		return nil
	}
	out := new(ApiTokenList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ApiTokenList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApiTokenSpec) DeepCopyInto(out *ApiTokenSpec) {
	*out = *in
	out.UnleashInstance = in.UnleashInstance
	if in.Projects != nil {
		in, out := &in.Projects, &out.Projects
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApiTokenSpec.
func (in *ApiTokenSpec) DeepCopy() *ApiTokenSpec {
	if in == nil {
		return nil
	}
	out := new(ApiTokenSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApiTokenStatus) DeepCopyInto(out *ApiTokenStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApiTokenStatus.
func (in *ApiTokenStatus) DeepCopy() *ApiTokenStatus {
	if in == nil {
		return nil
	}
	out := new(ApiTokenStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApiTokenUnleashInstance) DeepCopyInto(out *ApiTokenUnleashInstance) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApiTokenUnleashInstance.
func (in *ApiTokenUnleashInstance) DeepCopy() *ApiTokenUnleashInstance {
	if in == nil {
		return nil
	}
	out := new(ApiTokenUnleashInstance)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RemoteUnleash) DeepCopyInto(out *RemoteUnleash) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RemoteUnleash.
func (in *RemoteUnleash) DeepCopy() *RemoteUnleash {
	if in == nil {
		return nil
	}
	out := new(RemoteUnleash)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RemoteUnleash) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RemoteUnleashList) DeepCopyInto(out *RemoteUnleashList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]RemoteUnleash, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RemoteUnleashList.
func (in *RemoteUnleashList) DeepCopy() *RemoteUnleashList {
	if in == nil {
		return nil
	}
	out := new(RemoteUnleashList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RemoteUnleashList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RemoteUnleashSecret) DeepCopyInto(out *RemoteUnleashSecret) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RemoteUnleashSecret.
func (in *RemoteUnleashSecret) DeepCopy() *RemoteUnleashSecret {
	if in == nil {
		return nil
	}
	out := new(RemoteUnleashSecret)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RemoteUnleashServer) DeepCopyInto(out *RemoteUnleashServer) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RemoteUnleashServer.
func (in *RemoteUnleashServer) DeepCopy() *RemoteUnleashServer {
	if in == nil {
		return nil
	}
	out := new(RemoteUnleashServer)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RemoteUnleashSpec) DeepCopyInto(out *RemoteUnleashSpec) {
	*out = *in
	out.Server = in.Server
	out.AdminSecret = in.AdminSecret
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RemoteUnleashSpec.
func (in *RemoteUnleashSpec) DeepCopy() *RemoteUnleashSpec {
	if in == nil {
		return nil
	}
	out := new(RemoteUnleashSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RemoteUnleashStatus) DeepCopyInto(out *RemoteUnleashStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RemoteUnleashStatus.
func (in *RemoteUnleashStatus) DeepCopy() *RemoteUnleashStatus {
	if in == nil {
		return nil
	}
	out := new(RemoteUnleashStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Unleash) DeepCopyInto(out *Unleash) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Unleash.
func (in *Unleash) DeepCopy() *Unleash {
	if in == nil {
		return nil
	}
	out := new(Unleash)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Unleash) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashDatabaseConfig) DeepCopyInto(out *UnleashDatabaseConfig) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashDatabaseConfig.
func (in *UnleashDatabaseConfig) DeepCopy() *UnleashDatabaseConfig {
	if in == nil {
		return nil
	}
	out := new(UnleashDatabaseConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashFederationConfig) DeepCopyInto(out *UnleashFederationConfig) {
	*out = *in
	if in.Clusters != nil {
		in, out := &in.Clusters, &out.Clusters
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Namespaces != nil {
		in, out := &in.Namespaces, &out.Namespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashFederationConfig.
func (in *UnleashFederationConfig) DeepCopy() *UnleashFederationConfig {
	if in == nil {
		return nil
	}
	out := new(UnleashFederationConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashIngressConfig) DeepCopyInto(out *UnleashIngressConfig) {
	*out = *in
	if in.TLS != nil {
		in, out := &in.TLS, &out.TLS
		*out = new(UnleashIngressTLSConfig)
		**out = **in
	}
	if in.Annotations != nil {
		in, out := &in.Annotations, &out.Annotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashIngressConfig.
func (in *UnleashIngressConfig) DeepCopy() *UnleashIngressConfig {
	if in == nil {
		return nil
	}
	out := new(UnleashIngressConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashIngressTLSConfig) DeepCopyInto(out *UnleashIngressTLSConfig) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashIngressTLSConfig.
func (in *UnleashIngressTLSConfig) DeepCopy() *UnleashIngressTLSConfig {
	if in == nil {
		return nil
	}
	out := new(UnleashIngressTLSConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashList) DeepCopyInto(out *UnleashList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Unleash, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashList.
func (in *UnleashList) DeepCopy() *UnleashList {
	if in == nil {
		return nil
	}
	out := new(UnleashList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *UnleashList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashNetworkPolicyConfig) DeepCopyInto(out *UnleashNetworkPolicyConfig) {
	*out = *in
	if in.AllowAllFromNamespaces != nil {
		in, out := &in.AllowAllFromNamespaces, &out.AllowAllFromNamespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.ExtraIngressRules != nil {
		in, out := &in.ExtraIngressRules, &out.ExtraIngressRules
		*out = make([]networkingv1.NetworkPolicyIngressRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ExtraEgressRules != nil {
		in, out := &in.ExtraEgressRules, &out.ExtraEgressRules
		*out = make([]networkingv1.NetworkPolicyEgressRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashNetworkPolicyConfig.
func (in *UnleashNetworkPolicyConfig) DeepCopy() *UnleashNetworkPolicyConfig {
	if in == nil {
		return nil
	}
	out := new(UnleashNetworkPolicyConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashPrometheusConfig) DeepCopyInto(out *UnleashPrometheusConfig) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashPrometheusConfig.
func (in *UnleashPrometheusConfig) DeepCopy() *UnleashPrometheusConfig {
	if in == nil {
		return nil
	}
	out := new(UnleashPrometheusConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashSpec) DeepCopyInto(out *UnleashSpec) {
	*out = *in
	out.Prometheus = in.Prometheus
	out.Database = in.Database
	in.WebIngress.DeepCopyInto(&out.WebIngress)
	in.ApiIngress.DeepCopyInto(&out.ApiIngress)
	in.NetworkPolicy.DeepCopyInto(&out.NetworkPolicy)
	if in.ExtraEnvVars != nil {
		in, out := &in.ExtraEnvVars, &out.ExtraEnvVars
		*out = make([]corev1.EnvVar, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ExtraVolumes != nil {
		in, out := &in.ExtraVolumes, &out.ExtraVolumes
		*out = make([]corev1.Volume, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ExtraVolumeMounts != nil {
		in, out := &in.ExtraVolumeMounts, &out.ExtraVolumeMounts
		*out = make([]corev1.VolumeMount, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ExtraContainers != nil {
		in, out := &in.ExtraContainers, &out.ExtraContainers
		*out = make([]corev1.Container, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	in.Resources.DeepCopyInto(&out.Resources)
	in.Federation.DeepCopyInto(&out.Federation)
	if in.PodAnnotations != nil {
		in, out := &in.PodAnnotations, &out.PodAnnotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.PodLabels != nil {
		in, out := &in.PodLabels, &out.PodLabels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashSpec.
func (in *UnleashSpec) DeepCopy() *UnleashSpec {
	if in == nil {
		return nil
	}
	out := new(UnleashSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UnleashStatus) DeepCopyInto(out *UnleashStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UnleashStatus.
func (in *UnleashStatus) DeepCopy() *UnleashStatus {
	if in == nil {
		return nil
	}
	out := new(UnleashStatus)
	in.DeepCopyInto(out)
	return out
}
