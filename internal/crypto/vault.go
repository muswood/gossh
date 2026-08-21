// owner: muswood | Email: mumu920@outlook.com
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Vault struct {
	key     []byte
	keyPath string
}

func NewVault() (*Vault, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".gossh")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0700)
	keyPath := filepath.Join(dir, "master.key")

	var key []byte
	data, err := os.ReadFile(keyPath)
	if err == nil {
		switch len(data) {
		case 32:
			key = append([]byte(nil), data...)
		case 64:
			key, err = hex.DecodeString(strings.TrimSpace(string(data)))
			if err != nil || len(key) != 32 {
				return nil, fmt.Errorf("解析主密钥失败")
			}
		default:
			return nil, fmt.Errorf("主密钥长度无效: %d", len(data))
		}
	} else if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		if err := writeKeyFile(keyPath, key); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("读取主密钥失败: %w", err)
	}
	_ = os.Chmod(keyPath, 0600)

	return &Vault{key: key, keyPath: keyPath}, nil
}

func (v *Vault) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "v1:" + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (v *Vault) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	versioned := strings.HasPrefix(encoded, "v1:")
	data := strings.TrimPrefix(encoded, "v1:")
	ciphertext, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		if versioned {
			return "", fmt.Errorf("解密失败: 密文格式无效: %w", err)
		}
		return encoded, nil // 兼容导入的旧明文配置
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return "", errors.New("解密失败: 密钥不匹配")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		if versioned {
			return "", errors.New("解密失败: 密文长度无效")
		}
		return encoded, nil
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		if versioned {
			return "", errors.New("解密失败: 密钥不匹配或密文已损坏")
		}
		return encoded, nil // 兼容旧版本保存的明文或旧密文
	}
	return string(plaintext), nil
}

func writeKeyFile(path string, key []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".master.key-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
