package kubernetes

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/topfreegames/pitaya-bot/models"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CreateManagerPod will deploy a kubernetes pod containing a pitaya-bot manager
func CreateManagerPod(logger logrus.FieldLogger, clientset *kubernetes.Clientset, config *viper.Viper, specs []*models.Spec) {
	configMapClient := clientset.CoreV1().ConfigMaps(config.GetString("kubernetes.namespace"))
	deploymentsClient := clientset.CoreV1().Pods(config.GetString("kubernetes.namespace"))

	configMaps, err := configMapClient.List(metav1.ListOptions{LabelSelector: fmt.Sprintf("app=pitaya-bot-manager,game=%s", config.GetString("game"))})
	if err != nil {
		logger.Fatal(err)
	}
	if len(configMaps.Items) > 0 {
		return
	}

	configBinary, err := ioutil.ReadFile(config.ConfigFileUsed())
	if err != nil {
		logger.Fatal(err)
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "manager-config",
			Labels: map[string]string{
				"app":  "pitaya-bot-manager",
				"game": config.GetString("game"),
			},
		},
		BinaryData: map[string][]byte{"config.yaml": configBinary},
	}
	if _, err = configMapClient.Create(configMap); err != nil {
		logger.Fatal(err)
	}
	logger.Infof("Created manager configMap config.yaml")

	binData := make(map[string][]byte, len(specs))
	for _, spec := range specs {
		specBinary, err := ioutil.ReadFile(spec.Name)
		if err != nil {
			logger.Fatal(err)
		}
		specName := kubernetesAcceptedNamespace(filepath.Base(spec.Name))
		binData[specName] = specBinary
	}

	configMap = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "manager-specs",
			Labels: map[string]string{
				"app":  "pitaya-bot-manager",
				"game": config.GetString("game"),
			},
		},
		BinaryData: binData,
	}

	if _, err = configMapClient.Create(configMap); err != nil {
		logger.Fatal(err)
	}
	logger.Infof("Created manager configMap specs")

	deployment := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "manager",
			Labels: map[string]string{
				"app":  "pitaya-bot-manager",
				"game": config.GetString("game"),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyOnFailure,
			Containers: []corev1.Container{
				{
					Name:  "pitaya-bot-manager",
					Image: "tfgco/pitaya-bot:latest",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "manager-specs",
							MountPath: "/etc/pitaya-bot/specs",
						},
						{
							Name:      "manager-config",
							MountPath: "/etc/pitaya-bot",
						},
					},
					Command: []string{"./main"},
					Args:    []string{"run", "--config", "/etc/pitaya-bot/config.yaml", "-d", "/etc/pitaya-bot/specs", "-t", "remote-manager"},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "manager-specs",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "manager-specs"},
						},
					},
				},
				{
					Name: "manager-config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "manager-config"},
						},
					},
				},
			},
		},
	}

	if _, err := deploymentsClient.Create(deployment); err != nil {
		logger.Fatal(err)
	}
	logger.Infof("Created manager pod")
}

// DeployJobs will deploy as many kubernetes jobs as number of spec files
func DeployJobs(logger logrus.FieldLogger, clientset *kubernetes.Clientset, config *viper.Viper, specs []*models.Spec) {
	configMapClient := clientset.CoreV1().ConfigMaps(config.GetString("kubernetes.namespace"))
	deploymentsClient := clientset.BatchV1().Jobs(config.GetString("kubernetes.namespace"))

	configMaps, err := configMapClient.List(metav1.ListOptions{LabelSelector: fmt.Sprintf("app=pitaya-bot,game=%s", config.GetString("game"))})
	if err != nil {
		logger.Fatal(err)
	}
	if len(configMaps.Items) > 0 {
		return
	}

	configBinary, err := ioutil.ReadFile(config.ConfigFileUsed())
	if err != nil {
		logger.Fatal(err)
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config",
			Labels: map[string]string{
				"app":  "pitaya-bot",
				"game": config.GetString("game"),
			},
		},
		BinaryData: map[string][]byte{"config.yaml": configBinary},
	}

	if _, err = configMapClient.Create(configMap); err != nil {
		logger.Fatal(err)
	}
	logger.Infof("Created configMap config.yaml")

	for _, spec := range specs {
		specBinary, err := ioutil.ReadFile(spec.Name)
		if err != nil {
			logger.Fatal(err)
		}
		specName := kubernetesAcceptedNamespace(filepath.Base(spec.Name))

		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: specName,
				Labels: map[string]string{
					"app":  "pitaya-bot",
					"game": config.GetString("game"),
				},
			},
			BinaryData: map[string][]byte{"spec.json": specBinary},
		}

		if _, err = configMapClient.Create(configMap); err != nil {
			logger.Fatal(err)
		}
		logger.Infof("Created spec configMap %s", specName)

		deployment := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name: specName,
			},
			Spec: batchv1.JobSpec{
				//Parallelism: int32Ptr(1), TODO: Via config file, see how many bots are to be instantiated
				BackoffLimit: int32Ptr(config.GetInt32("kubernetes.job.retry")),
				Completions:  int32Ptr(1),
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app":  "pitaya-bot",
							"game": config.GetString("game"),
						},
					},
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:  "pitaya-bot",
								Image: "tfgco/pitaya-bot:latest",
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "spec",
										MountPath: "/etc/pitaya-bot/specs",
									},
									{
										Name:      "config",
										MountPath: "/etc/pitaya-bot",
									},
								},
							},
						},
						Volumes: []corev1.Volume{
							{
								Name: "spec",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: specName},
									},
								},
							},
							{
								Name: "config",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: "config"},
									},
								},
							},
						},
					},
				},
			},
		}

		if _, err := deploymentsClient.Create(deployment); err != nil {
			logger.Fatal(err)
		}
		logger.Infof("Created job %s", specName)
	}
}

// DeleteAll will delete all kubernetes resources that have been allocated to make the jobs
func DeleteAll(logger logrus.FieldLogger, clientset *kubernetes.Clientset, config *viper.Viper) {
	deleteAll(fmt.Sprintf("app=pitaya-bot,game=%s", config.GetString("game")), logger, clientset, config)
}

// DeleteAllManager will delete all pitaya-bot managers that have been allocated inside kubernetes cluster
func DeleteAllManager(logger logrus.FieldLogger, clientset *kubernetes.Clientset, config *viper.Viper) {
	deleteAll(fmt.Sprintf("app=pitaya-bot-manager,game=%s", config.GetString("game")), logger, clientset, config)
}

func deleteAll(labelSelector string, logger logrus.FieldLogger, clientset *kubernetes.Clientset, config *viper.Viper) {
	err := clientset.CoreV1().ConfigMaps(config.GetString("kubernetes.namespace")).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		logger.WithError(err).Error("Failed to delete configMaps")
	}
	logger.Infof("Deleted configMaps")

	err = clientset.BatchV1().Jobs(config.GetString("kubernetes.namespace")).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		logger.WithError(err).Error("Failed to delete jobs")
	}
	logger.Infof("Deleted jobs")

	err = clientset.CoreV1().Pods(config.GetString("kubernetes.namespace")).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		logger.WithError(err).Error("Failed to delete pods")
	}
	logger.Infof("Deleted pods")
}
