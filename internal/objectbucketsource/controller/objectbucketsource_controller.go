/*
Copyright 2026.

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

package controller

import (
	"context"
	"fmt"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	sourcesv1alpha1 "github.com/functions-dev/func-operator/api/sources/v1alpha1"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/config"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/s3client"
)

// AdapterConfig holds the notification configuration for a specific storage backend.
type AdapterConfig struct {
	ID                  string
	Topic               string
	StorageClassPattern *regexp.Regexp
}

var obcGVR = schema.GroupVersionResource{
	Group:    "objectbucket.io",
	Version:  "v1alpha1",
	Resource: "objectbucketclaims",
}

// ObjectBucketSourceReconciler reconciles an ObjectBucketSource object
type ObjectBucketSourceReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ConfigProvider config.ConfigProvider
}

// +kubebuilder:rbac:groups=sources.functions.dev,resources=objectbucketsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sources.functions.dev,resources=objectbucketsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sources.functions.dev,resources=objectbucketsources/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=objectbucket.io,resources=objectbucketclaims,verbs=get;list;watch

func (r *ObjectBucketSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var source sourcesv1alpha1.ObjectBucketSource
	if err := r.Get(ctx, req.NamespacedName, &source); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !source.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &source)
	}

	if !controllerutil.ContainsFinalizer(&source, sourcesv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(&source, sourcesv1alpha1.FinalizerName)
		if err := r.Update(ctx, &source); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	obcName := source.Spec.ObjectBucketClaim.Name
	ns := source.Namespace

	adapterCfg, err := r.resolveAdapterConfig(ctx, ns, obcName)
	if err != nil {
		log.Info("cannot resolve adapter config for OBC, requeuing", "obc", obcName, "error", err)
		r.setCondition(ctx, &source, sourcesv1alpha1.ConditionOBCCredentialsAvailable, metav1.ConditionFalse, "OBCNotReady", err.Error())
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	bucketHost, bucketName, bucketPort, err := r.readOBCConfigMap(ctx, ns, obcName)
	if err != nil {
		log.Info("OBC ConfigMap not available, requeuing", "obc", obcName, "error", err)
		r.setCondition(ctx, &source, sourcesv1alpha1.ConditionOBCCredentialsAvailable, metav1.ConditionFalse, "ConfigMapNotReady", err.Error())
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	accessKey, secretKey, err := r.readOBCSecret(ctx, ns, obcName)
	if err != nil {
		log.Info("OBC Secret not available, requeuing", "obc", obcName, "error", err)
		r.setCondition(ctx, &source, sourcesv1alpha1.ConditionOBCCredentialsAvailable, metav1.ConditionFalse, "SecretNotReady", err.Error())
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	r.setCondition(ctx, &source, sourcesv1alpha1.ConditionOBCCredentialsAvailable, metav1.ConditionTrue, "CredentialsAvailable", "OBC ConfigMap and Secret are available")

	mergedEvents, err := r.computeMergedEvents(ctx, ns, obcName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing merged events: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s:%s", bucketHost, bucketPort)
	s3c := s3client.NewS3Client(endpoint, accessKey, secretKey)

	if err := s3client.PutBucketNotification(ctx, s3c, bucketName, adapterCfg.ID, adapterCfg.Topic, mergedEvents); err != nil {
		log.Error(err, "failed to set bucket notification", "bucket", bucketName)
		r.setCondition(ctx, &source, sourcesv1alpha1.ConditionBucketNotificationSet, metav1.ConditionFalse, "PutNotificationFailed", err.Error())
		return ctrl.Result{}, err
	}

	log.Info("bucket notification set", "bucket", bucketName, "events", mergedEvents, "adapterID", adapterCfg.ID)
	r.setCondition(ctx, &source, sourcesv1alpha1.ConditionBucketNotificationSet, metav1.ConditionTrue, "NotificationConfigured", "Bucket notification configured successfully")

	return ctrl.Result{}, nil
}

func (r *ObjectBucketSourceReconciler) reconcileDelete(ctx context.Context, source *sourcesv1alpha1.ObjectBucketSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(source, sourcesv1alpha1.FinalizerName) {
		return ctrl.Result{}, nil
	}

	obcName := source.Spec.ObjectBucketClaim.Name
	ns := source.Namespace

	adapterCfg, cfgErr := r.resolveAdapterConfig(ctx, ns, obcName)
	if cfgErr != nil {
		log.Info("cannot resolve adapter config during deletion, removing finalizer anyway", "obc", obcName, "error", cfgErr)
	}

	bucketHost, bucketName, bucketPort, err := r.readOBCConfigMap(ctx, ns, obcName)
	if err != nil {
		log.Info("OBC ConfigMap not available during deletion, removing finalizer anyway", "obc", obcName)
	} else if cfgErr != nil {
		log.Info("adapter config not available during deletion, removing finalizer anyway", "obc", obcName)
	} else {
		accessKey, secretKey, secretErr := r.readOBCSecret(ctx, ns, obcName)
		if secretErr != nil {
			log.Info("OBC Secret not available during deletion, removing finalizer anyway", "obc", obcName)
		} else {
			mergedEvents, mergeErr := r.computeMergedEventsExcluding(ctx, ns, obcName, source.Name)
			if mergeErr != nil {
				log.Error(mergeErr, "computing merged events during deletion")
			} else {
				endpoint := fmt.Sprintf("https://%s:%s", bucketHost, bucketPort)
				s3c := s3client.NewS3Client(endpoint, accessKey, secretKey)

				if len(mergedEvents) == 0 {
					if err := s3client.RemoveBucketNotification(ctx, s3c, bucketName); err != nil {
						log.Error(err, "removing bucket notification during deletion", "bucket", bucketName)
					} else {
						log.Info("removed bucket notification", "bucket", bucketName)
					}
				} else {
					if err := s3client.PutBucketNotification(ctx, s3c, bucketName, adapterCfg.ID, adapterCfg.Topic, mergedEvents); err != nil {
						log.Error(err, "updating bucket notification during deletion", "bucket", bucketName)
					} else {
						log.Info("updated bucket notification after deletion", "bucket", bucketName, "events", mergedEvents)
					}
				}
			}
		}
	}

	controllerutil.RemoveFinalizer(source, sourcesv1alpha1.FinalizerName)
	if err := r.Update(ctx, source); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ObjectBucketSourceReconciler) readOBCConfigMap(ctx context.Context, namespace, name string) (host, bucketName, port string, err error) {
	var cm corev1.ConfigMap
	if err = r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cm); err != nil {
		return "", "", "", fmt.Errorf("getting ConfigMap %s/%s: %w", namespace, name, err)
	}

	host = cm.Data["BUCKET_HOST"]
	bucketName = cm.Data["BUCKET_NAME"]
	port = cm.Data["BUCKET_PORT"]

	if host == "" || bucketName == "" || port == "" {
		return "", "", "", fmt.Errorf("ConfigMap %s/%s missing required keys (BUCKET_HOST, BUCKET_NAME, BUCKET_PORT)", namespace, name)
	}
	return host, bucketName, port, nil
}

func (r *ObjectBucketSourceReconciler) readOBCSecret(ctx context.Context, namespace, name string) (accessKey, secretKey string, err error) {
	var secret corev1.Secret
	if err = r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		return "", "", fmt.Errorf("getting Secret %s/%s: %w", namespace, name, err)
	}

	accessKey = string(secret.Data["AWS_ACCESS_KEY_ID"])
	secretKey = string(secret.Data["AWS_SECRET_ACCESS_KEY"])

	if accessKey == "" || secretKey == "" {
		return "", "", fmt.Errorf("secret %s/%s missing required keys (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY)", namespace, name)
	}
	return accessKey, secretKey, nil
}

func (r *ObjectBucketSourceReconciler) computeMergedEvents(ctx context.Context, namespace, obcName string) ([]string, error) {
	return r.computeMergedEventsExcluding(ctx, namespace, obcName, "")
}

func (r *ObjectBucketSourceReconciler) computeMergedEventsExcluding(ctx context.Context, namespace, obcName, excludeName string) ([]string, error) {
	var list sourcesv1alpha1.ObjectBucketSourceList
	if err := r.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	eventSet := make(map[string]struct{})
	for _, t := range list.Items {
		if t.Spec.ObjectBucketClaim.Name != obcName {
			continue
		}
		if excludeName != "" && t.Name == excludeName {
			continue
		}
		if !t.DeletionTimestamp.IsZero() && t.Name != excludeName {
			continue
		}
		for _, e := range t.Spec.Events {
			eventSet[e] = struct{}{}
		}
	}

	events := make([]string, 0, len(eventSet))
	for e := range eventSet {
		events = append(events, e)
	}
	return events, nil
}

func (r *ObjectBucketSourceReconciler) readOBCStorageClassName(ctx context.Context, namespace, name string) (string, error) {
	var obc unstructured.Unstructured
	obc.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   obcGVR.Group,
		Version: obcGVR.Version,
		Kind:    "ObjectBucketClaim",
	})
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &obc); err != nil {
		return "", fmt.Errorf("getting ObjectBucketClaim %s/%s: %w", namespace, name, err)
	}
	sc, _, _ := unstructured.NestedString(obc.Object, "spec", "storageClassName")
	return sc, nil
}

func (r *ObjectBucketSourceReconciler) resolveAdapterConfig(ctx context.Context, namespace, obcName string) (*AdapterConfig, error) {
	cfg := r.ConfigProvider.GetConfig()

	adapterConfigs := []AdapterConfig{
		{
			ID:                  cfg.NoobaaAdapter.ID,
			Topic:               cfg.NoobaaAdapter.TopicARN,
			StorageClassPattern: cfg.NoobaaAdapter.StorageClassPattern,
		},
		{
			ID:                  cfg.RadosgwAdapter.ID,
			Topic:               cfg.RadosgwAdapter.TopicARN,
			StorageClassPattern: cfg.RadosgwAdapter.StorageClassPattern,
		},
	}

	if len(adapterConfigs) == 1 {
		return &adapterConfigs[0], nil
	}

	storageClass, err := r.readOBCStorageClassName(ctx, namespace, obcName)
	if err != nil {
		return nil, err
	}

	for i := range adapterConfigs {
		cfg := &adapterConfigs[i]
		if cfg.StorageClassPattern != nil && cfg.StorageClassPattern.MatchString(storageClass) {
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("no adapter config matches storageClassName %q for OBC %s/%s", storageClass, namespace, obcName)
}

func (r *ObjectBucketSourceReconciler) setCondition(ctx context.Context, source *sourcesv1alpha1.ObjectBucketSource, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&source.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: source.Generation,
	})
	if err := r.Status().Update(ctx, source); err != nil {
		logf.FromContext(ctx).Error(err, "updating status condition", "type", condType)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ObjectBucketSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sourcesv1alpha1.ObjectBucketSource{}).
		Named("objectbucketsource").
		Complete(r)
}
