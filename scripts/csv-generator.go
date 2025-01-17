/*


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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/blang/semver/v4"
	gyaml "github.com/ghodss/yaml"
	"github.com/kubevirt/tekton-tasks-operator/pkg/environment"
	"github.com/operator-framework/api/pkg/lib/version"
	csvv1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type generatorFlags struct {
	file               string
	dumpCRDs           bool
	removeCerts        bool
	namespace          string
	csvVersion         string
	pipelinesNamespace string
	operatorImage      string
	operatorVersion    string

	waitForVMIStatusImage  string
	modifyVMTemplateImage  string
	diskVirtSysprepImage   string
	diskVirtCustomizeImage string
	createVMImage          string
	modifyDataObjectImage  string
	copyTemplateImage      string
	cleanupVMImage         string
	generateSSHKeys        string
}

var (
	f       generatorFlags
	rootCmd = &cobra.Command{
		Use:   "csv-generator",
		Short: "csv-generator for ssp operator",
		Long:  `csv-generator generates deploy manifest for ssp operator`,
		Run: func(cmd *cobra.Command, args []string) {
			err := runGenerator()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}
)

func main() {
	rootCmd.Execute()
}

func init() {
	rootCmd.Flags().StringVar(&f.file, "file", "data/olm-catalog/tekton-tasks-operator.clusterserviceversion.yaml", "Location of the CSV yaml to modify")
	rootCmd.Flags().StringVar(&f.csvVersion, "csv-version", "", "Version of csv manifest (required)")
	rootCmd.Flags().StringVar(&f.namespace, "namespace", "", "Namespace in which tekton operator will be deployed (required)")
	rootCmd.Flags().StringVar(&f.pipelinesNamespace, "pipelines-namespace", "", "Namespace in which pipeline examples will be deployed")
	rootCmd.Flags().StringVar(&f.operatorImage, "operator-image", "", "Link to operator image (required)")
	rootCmd.Flags().StringVar(&f.operatorVersion, "operator-version", "", "Operator version (required)")
	rootCmd.Flags().StringVar(&f.waitForVMIStatusImage, "wait-for-vmi-status-image", "", "Link to wait-for-vmi-status task image")
	rootCmd.Flags().StringVar(&f.modifyVMTemplateImage, "modify-vm-template-image", "", "Link to modify-vm-template task image")
	rootCmd.Flags().StringVar(&f.diskVirtSysprepImage, "disk-virt-sysprep-image", "", "Link to disk-virt-sysprep task image")
	rootCmd.Flags().StringVar(&f.diskVirtCustomizeImage, "disk-virt-customize-image", "", "Link to disk-virt-customize task image")
	rootCmd.Flags().StringVar(&f.createVMImage, "create-vm-image", "", "Link to create-vm task image")
	rootCmd.Flags().StringVar(&f.modifyDataObjectImage, "modify-data-object-image", "", "Link to modify-data-object task image")
	rootCmd.Flags().StringVar(&f.copyTemplateImage, "copy-template-image", "", "Link to copy-template-image task image")
	rootCmd.Flags().StringVar(&f.generateSSHKeys, "generate-ssh-keys", "", "Link to generate-ssh-keys task image")
	rootCmd.Flags().StringVar(&f.cleanupVMImage, "cleanup-vm-image", "", "Link to cleanup-vm-image task image")

	rootCmd.Flags().BoolVar(&f.removeCerts, "webhook-remove-certs", false, "Remove the webhook certificate volume and mount")
	rootCmd.Flags().BoolVar(&f.dumpCRDs, "dump-crds", false, "Dump crds to stdout")

	rootCmd.MarkFlagRequired("csv-version")
	rootCmd.MarkFlagRequired("namespace")
	rootCmd.MarkFlagRequired("operator-image")
	rootCmd.MarkFlagRequired("operator-version")
}

func runGenerator() error {
	csvFile, err := ioutil.ReadFile(f.file)
	if err != nil {
		return err
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(csvFile), 1024)
	csv := csvv1.ClusterServiceVersion{}
	err = decoder.Decode(&csv)
	if err != nil {
		return err
	}

	err = replaceVariables(f, &csv)
	if err != nil {
		return err
	}

	if f.removeCerts {
		removeCerts(&csv)
	}

	relatedImages, err := buildRelatedImages(f)
	if err != nil {
		return err
	}

	err = marshallObject(csv, relatedImages, os.Stdout)
	if err != nil {
		return err
	}
	if !f.dumpCRDs {
		return nil
	}
	files, err := ioutil.ReadDir("data/crd")
	if err != nil {
		return err
	}
	for _, file := range files {
		crd := extv1beta1.CustomResourceDefinition{}

		err := readAndDecodeToCRD(file, &crd)
		if err != nil {
			return err
		}

		err = marshallObject(crd, nil, os.Stdout)
		if err != nil {
			return err
		}
	}
	return nil
}

func readAndDecodeToCRD(file os.FileInfo, crd *extv1beta1.CustomResourceDefinition) error {
	crdFile, err := ioutil.ReadFile(fmt.Sprintf("data/crd/%s", file.Name()))
	if err != nil {
		return err
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(crdFile), 1024)
	err = decoder.Decode(&crd)
	if err != nil {
		return err
	}
	return nil
}

func buildRelatedImage(imageDesc string, imageName string) (map[string]interface{}, error) {
	ri := make(map[string]interface{})
	ri["name"] = imageName
	ri["image"] = imageDesc

	return ri, nil
}

func buildRelatedImages(flags generatorFlags) ([]interface{}, error) {
	var relatedImages = make([]interface{}, 0)

	if flags.cleanupVMImage != "" {
		relatedImage, err := buildRelatedImage(flags.cleanupVMImage, "cleanup-vm")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.copyTemplateImage != "" {
		relatedImage, err := buildRelatedImage(flags.copyTemplateImage, "copy-template")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.generateSSHKeys != "" {
		relatedImage, err := buildRelatedImage(flags.generateSSHKeys, "generate-ssh-keys")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.modifyDataObjectImage != "" {
		relatedImage, err := buildRelatedImage(flags.modifyDataObjectImage, "modify-data-object")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.createVMImage != "" {
		relatedImage, err := buildRelatedImage(flags.createVMImage, "create-vm-from-manifest")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)

		relatedImage, err = buildRelatedImage(flags.createVMImage, "create-vm-from-template")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.diskVirtCustomizeImage != "" {
		relatedImage, err := buildRelatedImage(flags.diskVirtCustomizeImage, "disk-virt-customize")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.waitForVMIStatusImage != "" {
		relatedImage, err := buildRelatedImage(flags.waitForVMIStatusImage, "wait-for-vmi-status")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.modifyVMTemplateImage != "" {
		relatedImage, err := buildRelatedImage(flags.modifyVMTemplateImage, "modify-vm-template")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	if flags.diskVirtSysprepImage != "" {
		relatedImage, err := buildRelatedImage(flags.diskVirtSysprepImage, "disk-virt-sysprep")
		if err != nil {
			return nil, err
		}
		relatedImages = append(relatedImages, relatedImage)
	}

	return relatedImages, nil
}

func replaceVariables(flags generatorFlags, csv *csvv1.ClusterServiceVersion) error {
	csv.Name = "tekton-tasks-operator.v" + flags.csvVersion
	v, err := semver.New(flags.csvVersion)
	if err != nil {
		return err
	}
	csv.Spec.Version = version.OperatorVersion{
		Version: *v,
	}

	csv.Namespace = flags.namespace
	csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Namespace = flags.namespace
	templateSpec := &csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Spec
	for i, container := range templateSpec.Containers {
		updatedContainer := container
		if container.Name == "manager" {
			updatedContainer.Image = flags.operatorImage
			updatedContainer.Env = updateContainerEnvVars(flags, container)
			templateSpec.Containers[i] = updatedContainer
			break
		}
	}
	return nil
}

func updateContainerEnvVars(flags generatorFlags, container v1.Container) []v1.EnvVar {
	updatedVariables := make([]v1.EnvVar, 0)

	for _, envVariable := range container.Env {
		switch envVariable.Name {
		case environment.OperatorVersionKey:
			if flags.operatorVersion != "" {
				envVariable.Value = flags.operatorVersion
			}
		case environment.CleanupVMImageKey:
			if flags.cleanupVMImage != "" {
				envVariable.Value = flags.cleanupVMImage
			}
		case environment.CopyTemplateImageKey:
			if flags.copyTemplateImage != "" {
				envVariable.Value = flags.copyTemplateImage
			}
		case environment.ModifyDataObjectImageKey:
			if flags.modifyDataObjectImage != "" {
				envVariable.Value = flags.modifyDataObjectImage
			}
		case environment.CreateVMImageKey:
			if flags.createVMImage != "" {
				envVariable.Value = flags.createVMImage
			}
		case environment.DiskVirtCustomizeImageKey:
			if flags.diskVirtCustomizeImage != "" {
				envVariable.Value = flags.diskVirtCustomizeImage
			}
		case environment.DiskVirtSysprepImageKey:
			if flags.diskVirtSysprepImage != "" {
				envVariable.Value = flags.diskVirtSysprepImage
			}
		case environment.ModifyVMTemplateImageKey:
			if flags.modifyVMTemplateImage != "" {
				envVariable.Value = flags.modifyVMTemplateImage
			}
		case environment.WaitForVMISTatusImageKey:
			if flags.waitForVMIStatusImage != "" {
				envVariable.Value = flags.waitForVMIStatusImage
			}
		case environment.GenerateSSHKeysImageKey:
			if flags.generateSSHKeys != "" {
				envVariable.Value = flags.generateSSHKeys
			}
		}

		updatedVariables = append(updatedVariables, envVariable)
	}
	return updatedVariables
}

func removeCerts(csv *csvv1.ClusterServiceVersion) {
	// Remove the certs mount from the manager container
	templateSpec := &csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Spec
	for i, container := range templateSpec.Containers {
		if container.Name == "manager" {
			updatedVolumeMounts := templateSpec.Containers[i].VolumeMounts
			for j, volumeMount := range templateSpec.Containers[i].VolumeMounts {
				if volumeMount.Name == "cert" {
					updatedVolumeMounts = append(templateSpec.Containers[i].VolumeMounts[:j], templateSpec.Containers[i].VolumeMounts[j+1:]...)
					break
				}
			}
			templateSpec.Containers[i].VolumeMounts = updatedVolumeMounts
			break
		}
	}

	// Remove the cert volume definition
	updatedVolumes := templateSpec.Volumes
	for i, volume := range templateSpec.Volumes {
		if volume.Name == "cert" {
			updatedVolumes = append(templateSpec.Volumes[:i], templateSpec.Volumes[i+1:]...)
		}
	}
	templateSpec.Volumes = updatedVolumes
}

func marshallObject(obj interface{}, relatedImages []interface{}, writer io.Writer) error {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	var r unstructured.Unstructured
	if err := json.Unmarshal(jsonBytes, &r.Object); err != nil {
		return err
	}

	// remove status and metadata.creationTimestamp
	unstructured.RemoveNestedField(r.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "spec", "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "status")

	deployments, exists, err := unstructured.NestedSlice(r.Object, "spec", "install", "spec", "deployments")
	if err != nil {
		return err
	}

	if exists {
		for _, obj := range deployments {
			deployment := obj.(map[string]interface{})
			unstructured.RemoveNestedField(deployment, "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "spec", "template", "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "status")
		}
		unstructured.SetNestedSlice(r.Object, deployments, "spec", "install", "spec", "deployments")
	}

	if len(relatedImages) > 0 {
		unstructured.SetNestedSlice(r.Object, relatedImages, "spec", "relatedImages")
	}

	jsonBytes, err = json.Marshal(r.Object)
	if err != nil {
		return err
	}

	yamlBytes, err := gyaml.JSONToYAML(jsonBytes)
	if err != nil {
		return err
	}

	_, err = writer.Write([]byte("---\n"))
	if err != nil {
		return err
	}

	_, err = writer.Write(yamlBytes)
	if err != nil {
		return err
	}

	return nil
}
