// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"kpt.dev/configsync/pkg/api/configsync"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validateSecretExist validate the presence of secret in the cluster
func validateSecretExist(ctx context.Context, secretRef, namespace string, c client.Client) (*corev1.Secret, error) {
	sRef := client.ObjectKey{
		Name:      secretRef,
		Namespace: namespace,
	}

	secret := &corev1.Secret{}
	if err := c.Get(ctx, sRef, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, errors.Errorf(
				"secret %s not found: create one to allow client authentication", sRef)
		}
		return nil, errors.Wrapf(err,
			"secret %s get failed", sRef)
	}
	return secret, nil
}

// validateSecretData verify secret data for the given auth type.
func validateSecretData(auth configsync.AuthType, secret *corev1.Secret) error {
	switch auth {
	case configsync.AuthSSH:
		if _, ok := secret.Data[GitSecretConfigKeySSH]; !ok {
			return fmt.Errorf("git secretType was set as %q but ssh key is not present in %v secret", auth, secret.Name)
		}
	case configsync.AuthCookieFile:
		if _, ok := secret.Data[GitSecretConfigKeyCookieFile]; !ok {
			return fmt.Errorf("git secretType was set as %q but cookie_file key is not present in %v secret", auth, secret.Name)
		}
	case configsync.AuthToken:
		if _, ok := secret.Data[GitSecretConfigKeyToken]; !ok {
			return fmt.Errorf("git secretType was set as %q but token key is not present in %v secret", auth, secret.Name)
		}
		if _, ok := secret.Data[GitSecretConfigKeyTokenUsername]; !ok {
			return fmt.Errorf("git secretType was set as %q but username key is not present in %v secret", auth, secret.Name)
		}
	case configsync.AuthNone:
	case configsync.AuthGCENode:
	default:
		return fmt.Errorf("git secretType is set to unsupported value: %q", auth)
	}
	return nil
}
