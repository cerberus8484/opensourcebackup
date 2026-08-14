package catalog_test

import (
	"testing"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

func TestClassifyRepositoryBackend_IsDeterministicAndConservative(t *testing.T) {
	tests := []struct {
		name, repositoryType, location string
		want                           catalog.RepositoryBackendType
	}{
		{"rest server", "rest-server", "rest:https://repo.example", catalog.RepositoryBackendRESTServer},
		{"s3 location", "restic", "s3:bucket/path", catalog.RepositoryBackendS3},
		{"minio", "minio-s3", "s3:bucket/path", catalog.RepositoryBackendMinIO},
		{"filesystem", "nas-nfs", "/mnt/backup", catalog.RepositoryBackendFilesystem},
		{"unknown legacy", "restic", "sftp:host:/repo", catalog.RepositoryBackendUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := catalog.ClassifyRepositoryBackend(tt.repositoryType, tt.location); got != tt.want {
				t.Fatalf("ClassifyRepositoryBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeclaredSecurityPosture_NeverClaimsVerification(t *testing.T) {
	encryption := "aes256"
	posture := catalog.DeclaredSecurityPosture(&catalog.BackupRepository{
		EncryptionMode:    &encryption,
		ObjectLockEnabled: true,
		ImmutableMode:     catalog.ImmutableObjectLock,
	})
	if posture.Encryption != catalog.SecurityProvenanceDeclared || posture.ObjectLock != catalog.SecurityProvenanceDeclared || posture.Immutability != catalog.SecurityProvenanceDeclared {
		t.Fatalf("posture = %+v, want DECLARED for configured claims", posture)
	}
	if posture.Encryption == catalog.SecurityProvenanceVerified || posture.ObjectLock == catalog.SecurityProvenanceVerified || posture.Immutability == catalog.SecurityProvenanceVerified {
		t.Fatal("declarations must never be reported as VERIFIED")
	}
}

func TestCredentialPurpose_OnlyAllowsSeparationPurposes(t *testing.T) {
	for _, purpose := range []catalog.CredentialPurpose{
		catalog.CredentialPurposeBackupWrite,
		catalog.CredentialPurposeRestoreRead,
		catalog.CredentialPurposeRetention,
		catalog.CredentialPurposeRepositoryAdmin,
	} {
		if !purpose.Valid() {
			t.Fatalf("expected %q to be valid", purpose)
		}
	}
	if catalog.CredentialPurpose("DELETE_ALL").Valid() {
		t.Fatal("unexpected credential purpose accepted")
	}
}
