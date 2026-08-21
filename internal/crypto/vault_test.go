// owner: muswood | Email: mumu920@outlook.com
package crypto

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVaultEncryptDecrypt(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	v := &Vault{key: key}

	ciphertext, err := v.Encrypt("secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) < 3 || ciphertext[:3] != "v1:" {
		t.Fatalf("ciphertext is missing version prefix: %q", ciphertext)
	}
	plaintext, err := v.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "secret-value" {
		t.Fatalf("got %q, want %q", plaintext, "secret-value")
	}
}

func TestVaultRejectsCorruptVersionedCiphertext(t *testing.T) {
	v := &Vault{key: []byte("01234567890123456789012345678901")}
	if _, err := v.Decrypt("v1:not-valid"); err == nil {
		t.Fatal("expected corrupt ciphertext to fail")
	}
}

func TestWriteKeyFileUsesStableRawKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	key := []byte("01234567890123456789012345678901")
	if err := writeKeyFile(path, key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(key) {
		t.Fatalf("key file changed contents: %s", hex.EncodeToString(data))
	}
	if mode := data; len(mode) != 32 {
		t.Fatalf("got key length %d, want 32", len(mode))
	}
}
