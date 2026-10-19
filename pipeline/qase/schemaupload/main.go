// Command schemaupload creates or updates the Qase suites and cases described
// in the schemas directory. Run it after adding or renaming tests:
//
//	QASE_AUTOMATION_TOKEN=... go run ./pipeline/qase/schemaupload
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/rancher/steve-tests/extensions/qase"
)

func main() {
	basePath := flag.String("basepath", "schemas", "directory holding *schemas.yaml files")
	prefix := flag.String("schema-prefix", os.Getenv(qase.SchemaPrefixEnvVar), "only upload files named <prefix>_*schemas.yaml")
	project := flag.String("project", os.Getenv(qase.ProjectIDEnvVar), "require every schema to target only this project (defaults to QASE_PROJECT_ID)")
	flag.Parse()

	if os.Getenv(qase.QaseTokenEnvVar) == "" {
		slog.Error("Qase token is not set", "env", qase.QaseTokenEnvVar)
		os.Exit(1)
	}

	if *project != "" {
		schemas, err := qase.GetSchemasByPrefix(*basePath, *prefix)
		if err == nil {
			err = validateProject(schemas, *project)
		}
		if err != nil {
			slog.Error("schema project validation failed", "error", err)
			os.Exit(1)
		}
	}

	if err := qase.UploadSchemas(context.Background(), qase.SetupQaseClient(), *basePath, *prefix); err != nil {
		slog.Error("schema upload failed", "error", err)
		os.Exit(1)
	}
	slog.Info("schema upload finished")
}

// A configured project is a guard, not an override of the schema's targets.
// Validate every suite before uploading so a mismatch cannot partially write.
func validateProject(schemas []qase.TestSuiteSchema, project string) error {
	if len(schemas) == 0 {
		return fmt.Errorf("no schemas found for project %s", project)
	}
	for _, suite := range schemas {
		if len(suite.Projects) != 1 || suite.Projects[0] != project {
			return fmt.Errorf("suite %q targets %v, but the configured Qase project is %q; update the schema projects or QASE_PROJECT_ID", suite.Suite, suite.Projects, project)
		}
	}
	return nil
}
