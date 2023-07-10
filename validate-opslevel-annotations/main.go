/*
validate-opslevel-annotations checks the OpsLevel annotations for a list of
manifests are valid.

It will process an entire manifest file and report on all errors across all
objects therein, though it will bail upon the first manifest it can't read or
can't interpret. This is meant to run on the output of 'kustomize-build-dirs'
and hence expects manifests filenames to be base64 encoded paths.

Usage:

	validate-opslevel-annotations [ manifest-file] ...

Example flow:

	$ mkdir built-manifests
	$ git diff --diff-filter d --name-only main | xargs kustomize-build-dirs --out-dir manifests --
	$ validate-opslevel-annotations build-dir/*
*/
package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/streaming"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	if err := validateOpsLevelAnnotationsForManifests(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "failed: %v\n", err)
		os.Exit(1)
	}
}

func validateOpsLevelAnnotationsForManifests(manifestFiles []string) error {
	var errString string

	for _, path := range manifestFiles {
		rawManifestFile, err := base64.StdEncoding.DecodeString(filepath.Base(path))
		if err != nil {
			return fmt.Errorf("expected a base64 encoded filename at %s, but failed to decode: %v", path, err)
		}
		// decoded path to the kustomize build directory, use this to
		// communicate errors etc.
		manifestFile := string(rawManifestFile)

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("Failed opening manifest: %s: %v", manifestFile, err)
		}
		defer file.Close()

		objects, err := decodeManifest(file)
		if err != nil {
			return fmt.Errorf("Failed reading manifests from %s: %v", manifestFile, err)
		}

		var objectErrStrings []string
		for _, object := range objects {
			if err := validateOpsLevelAnnotations(object); err != nil {
				objectErrStrings = append(
					objectErrStrings,
					fmt.Sprintf(
						"invalid %s: %s: %v",
						object.GetObjectKind().GroupVersionKind().Kind,
						object.GetName(),
						err,
					),
				)
			}
		}
		if len(objectErrStrings) != 0 {
			errString += fmt.Sprintf(
				"failed validating manifests from %s: %s",
				manifestFile,
				strings.Join(objectErrStrings, "\n"),
			)
		}
	}

	if errString != "" {
		return errors.New(errString)
	}

	return nil
}

func validateOpsLevelAnnotations(object client.Object) error {
	annotations := object.GetAnnotations()

	missingRequiredAnnotations := getMissingRequiredAnnotations(annotations)
	missingPrefixAnnotations := getMissingRequiredPrefixAnnotations(annotations)

	var errStrings []string
	if len(missingRequiredAnnotations) != 0 {
		errStrings = append(
			errStrings,
			fmt.Sprintf(
				"Missing required annotations:\n\t%s",
				strings.Join(missingRequiredAnnotations, "\n\t"),
			),
		)
	}
	if len(missingPrefixAnnotations) != 0 {
		errStrings = append(
			errStrings,
			fmt.Sprintf(
				"No annotation found with required prefixes:\n\t%s",
				strings.Join(missingPrefixAnnotations, "\n\t"),
			),
		)
	}

	if len(errStrings) != 0 {
		return errors.New(strings.Join(errStrings, "\n"))
	}
	return nil
}

func getMissingRequiredAnnotations(annotations map[string]string) []string {
	requiredAnnotations := []string{
		"app.uw.systems/description",
		"app.uw.systems/tier",
	}

	var missingAnnotations []string
	for _, requiredAnnotation := range requiredAnnotations {
		if _, ok := annotations[requiredAnnotation]; !ok {
			missingAnnotations = append(missingAnnotations, requiredAnnotation)
		}
	}

	return missingAnnotations
}

func getMissingRequiredPrefixAnnotations(annotations map[string]string) []string {
	seenPrefixAnnotations := map[string]bool{"app.uw.systems/repos": false}

	for annotation := range annotations {
		for prefix := range seenPrefixAnnotations {
			if strings.HasPrefix(annotation, prefix) {
				seenPrefixAnnotations[prefix] = true
			}
		}
	}

	var missingAnnotations []string
	for annotationPrefix, seen := range seenPrefixAnnotations {
		if !seen {
			missingAnnotations = append(missingAnnotations, annotationPrefix)
		}
	}

	// sort for a consistent output
	sort.Strings(missingAnnotations)
	return missingAnnotations
}

// decodeManifests decodes a manifestfile containing manifests for 1 or more
// k8s objects
func decodeManifest(rc io.ReadCloser) ([]client.Object, error) {
	yamlDecoder := yaml.NewDocumentDecoder(rc)
	streamDecoder := streaming.NewDecoder(yamlDecoder, scheme.Codecs.UniversalDeserializer())
	defer streamDecoder.Close()

	var objects []client.Object
	for {
		object, _, err := streamDecoder.Decode(nil, nil)
		if err != nil {
			switch {
			case err == io.EOF:
				return objects, nil
			case runtime.IsNotRegisteredError(err):
				// we don't want to try and cover all possible APIs
				// we're only interested in a handful of builtin ones
				continue
			default:
				return nil, err
			}
		}

		if isSupportedObject(object) {
			objects = append(objects, object.(client.Object))
		}
	}
}

func isSupportedObject(object runtime.Object) bool {
	kind := object.GetObjectKind().GroupVersionKind().Kind
	group := object.GetObjectKind().GroupVersionKind().Group

	return (group == "batch" && kind == "CronJob") ||
		(group == "apps" && (kind == "Deployment" || kind == "StatefulSet"))
}
