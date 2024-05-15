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
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var unleashlog = logf.Log.WithName("unleash-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *Unleash) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-unleash-nais-io-v1-unleash,mutating=true,failurePolicy=fail,sideEffects=None,groups=unleash.nais.io,resources=unleashes,verbs=create;update,versions=v1,name=munleash.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &Unleash{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *Unleash) Default() {
	unleashlog.Info("default", "name", r.Name)

	// TODO(user): fill in your defaulting logic.
}
