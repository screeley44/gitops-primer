/*
Copyright 2021.

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

package controllers

import (
	"context"

	"github.com/operator-framework/operator-lib/status"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	primerv1alpha1 "github.com/cooktheryan/gitops-primer/api/v1alpha1"
)

// ExtractReconciler reconciles a Extract object
type ExtractReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=primer.gitops.io,resources=extracts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=primer.gitops.io,resources=extracts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=primer.gitops.io,resources=extracts/finalizers,verbs=update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=*,resources=*,verbs=get;list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Extract object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ExtractReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the Extract instance
	instance := &primerv1alpha1.Extract{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		if instance.Status.Completed {
			return ctrl.Result{}, nil
		}
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Extract resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Extract")
		updateErrCondition(instance, err)
		return ctrl.Result{}, err
	}

	// Check if the Job already exists, if not create a new one
	found := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: "primer-extract-" + instance.Name, Namespace: instance.Namespace}, found); err != nil {
		if instance.Status.Completed {
			return ctrl.Result{}, nil
		}
		if errors.IsNotFound(err) {
			if instance.Spec.Method == "git" {
				// Define a new job
				job := r.jobGitForExtract(instance)
				log.Info("Creating a new Job", "Job.Namespace", job.Namespace, "Job.Name", job.Name)
				if err = r.Create(ctx, job); err != nil {
					log.Error(err, "Failed to create new Job", "Job.Namespace", job.Namespace, "Job.Name", job.Name)
					updateErrCondition(instance, err)
					return ctrl.Result{}, err
				}
				// Job created successfully - return and requeue
				return ctrl.Result{Requeue: true}, nil
			} else if instance.Spec.Method == "download" {
				// Define a new job
				job := r.jobDownloadForExtract(instance)
				log.Info("Creating a new Job", "Job.Namespace", job.Namespace, "Job.Name", job.Name)
				if err = r.Create(ctx, job); err != nil {
					log.Error(err, "Failed to create new Job", "Job.Namespace", job.Namespace, "Job.Name", job.Name)
					updateErrCondition(instance, err)
					return ctrl.Result{}, err
				}
				// Job created successfully - return and requeue
				return ctrl.Result{Requeue: true}, nil
			}
		}
		log.Error(err, "Failed to get Job")
		updateErrCondition(instance, err)
		return ctrl.Result{}, err
	}

	// Check if the Service Account already exists, if not create a new one
	foundSA := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: "primer-extract-" + instance.Name, Namespace: instance.Namespace}, foundSA); err != nil {
		if instance.Status.Completed {
			return ctrl.Result{}, nil
		}
		if errors.IsNotFound(err) {
			// Define a new Service Account
			serviceAcct := r.saGenerate(instance)
			log.Info("Creating a new Service Account", "serviceAcct.Namespace", serviceAcct.Namespace, "serviceAcct.Name", serviceAcct.Name)
			if err := r.Create(ctx, serviceAcct); err != nil {
				log.Error(err, "Failed to create new Service Account", "serviceAcct.Namespace", serviceAcct.Namespace, "serviceAcct.Name", serviceAcct.Name)

				updateErrCondition(instance, err)
				return ctrl.Result{}, err
			}
			// Service Account created successfully - return and requeue
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to get Service Account")
		updateErrCondition(instance, err)
		return ctrl.Result{}, err
	}

	// Check if the Role already exists, if not create a new one
	foundRole := &rbacv1.Role{}
	if err := r.Get(ctx, types.NamespacedName{Name: "primer-extract-" + instance.Name, Namespace: instance.Namespace}, foundRole); err != nil {
		if instance.Status.Completed {
			return ctrl.Result{}, nil
		}
		if errors.IsNotFound(err) {
			// Define a new Role
			role := r.roleGenerate(instance)
			log.Info("Creating a new Role", "role.Namespace", role.Namespace, "role.Name", role.Name)
			if err := r.Create(ctx, role); err != nil {
				log.Error(err, "Failed to create new Role", "role.Namespace", role.Namespace, "role.Name", role.Name)
				updateErrCondition(instance, err)
				return ctrl.Result{}, err
			}
			// Role created successfully - return and requeue
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to get Role")
		updateErrCondition(instance, err)
		return ctrl.Result{}, err
	}

	// Check if the RoleBinding already exists, if not create a new one
	foundRoleBinding := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: "primer-extract-" + instance.Name, Namespace: instance.Namespace}, foundRoleBinding); err != nil {
		if instance.Status.Completed {
			return ctrl.Result{}, nil
		}
		if errors.IsNotFound(err) {
			// Define a new Role Binding
			roleBinding := r.roleBindingGenerate(instance)
			log.Info("Creating a new Role Binding", "roleBinding.Namespace", roleBinding.Namespace, "roleBinding.Name", roleBinding.Name)
			if err := r.Create(ctx, roleBinding); err != nil {
				log.Error(err, "Failed to create new Role Binding", "roleBinding.Namespace", roleBinding.Namespace, "roleBinding.Name", roleBinding.Name)
				updateErrCondition(instance, err)
				return ctrl.Result{}, err
			}
			// Role Binding created successfully - return and requeue
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to get Role Binding")
		updateErrCondition(instance, err)
		return ctrl.Result{}, err
	}

	// Check if the Service Account already exists, if not create a new one
	foundVolume := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, types.NamespacedName{Name: "primer-extract-" + instance.Name, Namespace: instance.Namespace}, foundVolume); err != nil {
		if instance.Status.Completed {
			return ctrl.Result{}, nil
		}
		if errors.IsNotFound(err) {
			// Define a new PVC
			persistentVC := r.pvcGenerate(instance)
			log.Info("Creating a new Service Account", "persistentVC.Namespace", persistentVC.Namespace, "persistentVC.Name", persistentVC.Name)
			if err := r.Create(ctx, persistentVC); err != nil {
				log.Error(err, "Failed to create a PVC", "persistentVC.Namespace", persistentVC.Namespace, "persistentVC.Name", persistentVC.Name)

				updateErrCondition(instance, err)
				return ctrl.Result{}, err
			}
			// Persistent Volume created successfully - return and requeue
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to get PVC")
		updateErrCondition(instance, err)
		return ctrl.Result{}, err
	}

	if instance.Status.Conditions == nil {
		instance.Status.Conditions = status.Conditions{}
	}

	// Update status.Nodes if needed
	instance.Status.Completed = isJobComplete(found)
	if instance.Status.Completed {
		log.Info("Job completed")
		log.Info("Cleaning up Primer Resources")
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update Extract status")
			updateErrCondition(instance, err)
			return ctrl.Result{}, err
		}
		r.Delete(ctx, found, client.PropagationPolicy(metav1.DeletePropagationBackground))
		r.Delete(ctx, foundRole)
		r.Delete(ctx, foundRoleBinding)
		r.Delete(ctx, foundSA)

		// Set reconcile status condition complete
		instance.Status.Conditions.SetCondition(
			status.Condition{
				Type:    primerv1alpha1.ConditionReconciled,
				Status:  corev1.ConditionTrue,
				Reason:  primerv1alpha1.ReconciledReasonComplete,
				Message: "Reconcile complete",
			})
	}
	return ctrl.Result{}, nil
}

func updateErrCondition(instance *primerv1alpha1.Extract, err error) {
	instance.Status.Conditions.SetCondition(
		status.Condition{
			Type:    primerv1alpha1.ConditionReconciled,
			Status:  corev1.ConditionFalse,
			Reason:  primerv1alpha1.ReconciledReasonError,
			Message: err.Error(),
		})
}

// jobGitForExtract returns a instance Job object
func (r *ExtractReconciler) jobGitForExtract(m *primerv1alpha1.Extract) *batchv1.Job {
	mode := int32(0644)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "primer-extract-" + m.Name,
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      "Never",
					ServiceAccountName: "primer-extract-" + m.Name,
					Containers: []corev1.Container{{
						Name:            m.Name,
						ImagePullPolicy: "IfNotPresent",
						Image:           "quay.io/octo-emerging/gitops-primer-extract:latest",
						Command:         []string{"/bin/sh", "-c", "/committer.sh"},
						Env: []corev1.EnvVar{
							{Name: "REPO", Value: m.Spec.Repo},
							{Name: "BRANCH", Value: m.Spec.Branch},
							{Name: "EMAIL", Value: m.Spec.Email},
							{Name: "NAMESPACE", Value: m.Namespace},
							{Name: "METHOD", Value: m.Spec.Method},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "sshkeys", MountPath: "/keys"},
							{Name: "output", MountPath: "/output"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "output", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: "primer-extract-" + m.Name,
							},
						},
						},
						{Name: "sshkeys", VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  m.Spec.Secret,
								DefaultMode: &mode,
							}},
						},
					},
				},
			},
		},
	}
	ctrl.SetControllerReference(m, job, r.Scheme)
	return job
}

// jobGitForExtract returns a instance Job object
func (r *ExtractReconciler) jobDownloadForExtract(m *primerv1alpha1.Extract) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "primer-extract-" + m.Name,
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      "Never",
					ServiceAccountName: "primer-extract-" + m.Name,
					Containers: []corev1.Container{{
						Name:            m.Name,
						ImagePullPolicy: "IfNotPresent",
						Image:           "quay.io/octo-emerging/gitops-primer-extract:latest",
						Command:         []string{"/bin/sh", "-c", "/committer.sh"},
						Env: []corev1.EnvVar{
							{Name: "METHOD", Value: m.Spec.Method},
							{Name: "NAMESPACE", Value: m.Namespace},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "output", MountPath: "/output"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "output", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: "primer-extract-" + m.Name,
							},
						},
						},
					},
				},
			},
		},
	}
	ctrl.SetControllerReference(m, job, r.Scheme)
	return job
}

func (r *ExtractReconciler) saGenerate(m *primerv1alpha1.Extract) *corev1.ServiceAccount {
	// Define a new Service Account object
	serviceAcct := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "primer-extract-" + m.Name,
			Namespace: m.Namespace,
		},
	}
	// Service reconcile finished
	ctrl.SetControllerReference(m, serviceAcct, r.Scheme)
	return serviceAcct
}

func (r *ExtractReconciler) pvcGenerate(m *primerv1alpha1.Extract) *corev1.PersistentVolumeClaim {
	// Define a new PVC object
	persistentVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "primer-extract-" + m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("1Gi"),
				},
			},
		},
	}
	// Service reconcile finished
	ctrl.SetControllerReference(m, persistentVC, r.Scheme)
	return persistentVC
}

func (r *ExtractReconciler) roleGenerate(m *primerv1alpha1.Extract) *rbacv1.Role {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "primer-extract-" + m.Name,
			Namespace: m.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list"},
			},
		},
	}
	// Service reconcile finished
	ctrl.SetControllerReference(m, role, r.Scheme)
	return role
}

func (r *ExtractReconciler) roleBindingGenerate(m *primerv1alpha1.Extract) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "primer-extract-" + m.Name,
			Namespace: m.Namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Name:     "primer-extract-" + m.Name,
			Kind:     "Role",
		},
		Subjects: []rbacv1.Subject{
			{Kind: "ServiceAccount", Name: "primer-extract-" + m.Name},
		},
	}
	// Service reconcile finished
	ctrl.SetControllerReference(m, roleBinding, r.Scheme)
	return roleBinding
}

func isJobComplete(job *batchv1.Job) bool {
	return job.Status.Succeeded == 1
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExtractReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&primerv1alpha1.Extract{}).
		Owns(&batchv1.Job{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}
