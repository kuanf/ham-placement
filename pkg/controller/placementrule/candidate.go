// Copyright 2019 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package placementrule

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"

	dplycorev1alpha1 "github.com/hybridapp-io/ham-deployable-operator/pkg/apis/core/v1alpha1"
	corev1alpha1 "github.com/hybridapp-io/ham-placement/pkg/apis/core/v1alpha1"
)

func convertMetaGVRToScheme(mgvr *metav1.GroupVersionResource) *schema.GroupVersionResource {
	if mgvr == nil {
		return nil
	}

	return &schema.GroupVersionResource{
		Group:    mgvr.Group,
		Version:  mgvr.Version,
		Resource: mgvr.Resource,
	}
}

// check deployer by type everytime. could cache it.
func (r *ReconcilePlacementRule) getTargetGVR(instance *corev1alpha1.PlacementRule) (*schema.GroupVersionResource, error) {
	dplylist := &dplycorev1alpha1.DeployerList{}

	// do not check the default deployertyp here in case user wants to override target for default deployer type
	if instance.Spec.DeployerType == nil {
		return convertMetaGVRToScheme(dplycorev1alpha1.DefaultKubernetesPlacementTarget), nil
	}

	dplytype := *instance.Spec.DeployerType

	err := r.client.List(context.TODO(), dplylist)
	if err != nil {
		klog.Error("Failed to list deployers in system wit error: ", err)
		return nil, err
	}

	for _, dply := range dplylist.Items {
		if dply.Spec.Type == dplytype {
			if dply.Spec.PlacementTarget != nil {
				return convertMetaGVRToScheme(dply.Spec.PlacementTarget), nil
			}
			// default to deployer type
			return convertMetaGVRToScheme(dplycorev1alpha1.DeployerPlacementTarget), nil
		}
	}

	// no match, now its the time to check default deployer type
	if dplytype == dplycorev1alpha1.DefaultDeployerType {
		return convertMetaGVRToScheme(dplycorev1alpha1.DefaultKubernetesPlacementTarget), nil
	}

	return nil, nil
}

func (r *ReconcilePlacementRule) generateCandidates(instance *corev1alpha1.PlacementRule) ([]corev1.ObjectReference, error) {
	if instance == nil {
		return nil, nil
	}

	var candiates []corev1.ObjectReference

	// select by targetLabels, nil = everything
	listopts := metav1.ListOptions{}

	if instance.Spec.TargetLabels != nil {
		selector, err := metav1.LabelSelectorAsSelector(instance.Spec.TargetLabels)
		if err != nil {
			klog.Error("Failed to parse label selector with error: ", err)
			return nil, err
		}

		listopts.LabelSelector = selector.String()
	}

	gvr, err := r.getTargetGVR(instance)
	if gvr == nil {
		klog.Error("Failed to get target GroupVersionResource for placement rule with error: ", err)
		return nil, err
	}

	tl, err := r.dynamicClient.Resource(*gvr).List(context.TODO(), listopts)
	if err != nil {
		klog.Error("Failed to list ", gvr.String(), " with error: ", err)
		return nil, err
	}

	// build candidate list, filter targets, nil = everything
	for _, obj := range tl.Items {
		or := corev1.ObjectReference{
			Kind:       obj.GroupVersionKind().Kind,
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			APIVersion: obj.GetAPIVersion(),
			UID:        obj.GetUID(),
		}

		pass := true

		// check targets
		if len(instance.Spec.Targets) > 0 {
			pass = false
		}

		for _, t := range instance.Spec.Targets {
			if t.Name != "" && t.Name != or.Name {
				continue
			}

			if t.Namespace != "" && t.Namespace != or.Namespace {
				continue
			}

			pass = true

			break
		}

		if pass {
			candiates = append(candiates, or)
		}
	}

	return candiates, nil
}

func isSameCandidateList(candidates []corev1.ObjectReference, instance *corev1alpha1.PlacementRule) bool {
	if candidates == nil && instance == nil {
		return true
	}

	if candidates == nil || instance == nil {
		return false
	}

	newmap := make(map[types.UID]bool)
	// generate map for src
	for _, or := range candidates {
		newmap[or.UID] = true
	}

	exarray := instance.Status.Candidates
	if len(exarray) > 0 {
		for _, or := range exarray {
			if _, ok := newmap[or.UID]; !ok {
				return false
			}

			delete(newmap, or.UID)
		}
	}

	exarray = instance.Status.Eliminators
	if len(exarray) > 0 {
		for _, or := range exarray {
			if _, ok := newmap[or.UID]; !ok {
				return false
			}

			delete(newmap, or.UID)
		}
	}

	return len(newmap) == 0
}
