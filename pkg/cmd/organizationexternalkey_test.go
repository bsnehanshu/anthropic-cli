package cmd

import (
	"testing"

	"github.com/anthropics/anthropic-cli/v2/internal/mocktest"
)

func TestOrganizationExternalKeysCreate(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:external-keys", "create",
			"--provider-config", "{kms_arn: arn:aws:kms:us-east-1:111122223333:key/abcd1234-5678-90ab-cdef-000011112222, type: aws, region: us-east-1}",
			"--display-name", "x",
			"--geo", "us",
			"--user-profile-id", "uprof_01SDCCSbTxrXDpWc1phhtcfK",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"provider_config:\n" +
			"  kms_arn: arn:aws:kms:us-east-1:111122223333:key/abcd1234-5678-90ab-cdef-000011112222\n" +
			"  type: aws\n" +
			"  region: us-east-1\n" +
			"display_name: x\n" +
			"geo: us\n" +
			"user_profile_id: uprof_01SDCCSbTxrXDpWc1phhtcfK\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"organization:external-keys", "create",
		)
	})
}

func TestOrganizationExternalKeysRetrieve(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:external-keys", "retrieve",
			"--external-key-id", "external_key_id",
		)
	})
}

func TestOrganizationExternalKeysUpdate(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:external-keys", "update",
			"--external-key-id", "external_key_id",
			"--display-name", "x",
			"--geo", "us",
			"--provider-config", "{kms_arn: arn:aws:kms:us-east-1:111122223333:key/abcd1234-5678-90ab-cdef-000011112222, type: aws, region: us-east-1}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"display_name: x\n" +
			"geo: us\n" +
			"provider_config:\n" +
			"  kms_arn: arn:aws:kms:us-east-1:111122223333:key/abcd1234-5678-90ab-cdef-000011112222\n" +
			"  type: aws\n" +
			"  region: us-east-1\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"organization:external-keys", "update",
			"--external-key-id", "external_key_id",
		)
	})
}

func TestOrganizationExternalKeysList(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:external-keys", "list",
			"--max-items", "10",
			"--limit", "1",
			"--page", "page",
		)
	})
}

func TestOrganizationExternalKeysDelete(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:external-keys", "delete",
			"--external-key-id", "external_key_id",
		)
	})
}

func TestOrganizationExternalKeysValidate(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:external-keys", "validate",
			"--external-key-id", "external_key_id",
		)
	})
}
