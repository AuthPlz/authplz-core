package backup

import (
	"crypto/rand"
	"fmt"
	"log"
	"strings"
)

import (
	mnemonics "github.com/NebulousLabs/entropy-mnemonics"
	"golang.org/x/crypto/bcrypt"
)

const (
	recoveryKeyLen   = 128 / 8
	recoveryNameLen  = 3
	numRecoveryKeys  = 5
	backupHashRounds = 12
)

// Controller Backup code controller instance
// The backup code controller generates and parses mnemonic backup codes for 2fa use
// These codes can be registered and used in the same manner as any other 2fa component.
type Controller struct {
	issuerName  string
	backupStore Storer
}

// NewController creates a new backup code controller
// Backup tokens are issued with an associated issuer name to assist with user identification of codes.
// A Storer provides underlying storage to the backup code module
func NewController(issuerName string, backupStore Storer) *Controller {
	return &Controller{
		issuerName:  issuerName,
		backupStore: backupStore,
	}
}

func cryptoBytes(size int) ([]byte, error) {
	buf := make([]byte, size)
	n, err := rand.Read(buf)
	if err != nil {
		return buf, err
	}
	if n != recoveryKeyLen {
		return buf, fmt.Errorf("BackupController.CreateCodes entropy error")
	}
	return buf, nil
}

// BackupKey structure for API use
type BackupKey struct {
	// Mnemonic key name
	Name string
	// Mnemonic key code
	Code string
	// Key Hash
	Hash string
}

// CodeResponse is the backup code response object returned when codes are created
type CodeResponse struct {
	Keys []BackupKey
}

// CreateCodes creates a set of backup codes for a user
// TODO: should this erase existing codes?
func (bc *Controller) CreateCodes(userid string) ([]BackupKey, error) {
	keys := make([]BackupKey, numRecoveryKeys)

	// Generate raw codes
	for i := range keys {
		// Generate random key

		code, err := cryptoBytes(recoveryKeyLen)
		if err != nil {
			return keys, err
		}

		name, err := cryptoBytes(recoveryNameLen)
		if err != nil {
			return keys, err
		}

		// Generate mnemonic codes
		mnemonicCode, err := mnemonics.ToPhrase(code, mnemonics.English)
		if err != nil {
			return keys, err
		}

		mnemonicName, err := mnemonics.ToPhrase(name, mnemonics.English)
		if err != nil {
			return keys, err
		}

		// Generate hashes
		hash, err := bcrypt.GenerateFromPassword([]byte(code), backupHashRounds)
		if err != nil {
			return keys, err
		}

		keys[i] = BackupKey{mnemonicName.String(), mnemonicCode.String(), string(hash)}
	}

	// Save to database
	for _, key := range keys {
		_, err := bc.backupStore.AddBackupCode(userid, key.Name, key.Hash)
		if err != nil {
			return keys, err
		}
	}

	return keys, nil
}

// IsSupported checks whether the backup code method is supported
func (bc *Controller) IsSupported(userid string) bool {
	// Fetch codes for a user
	codes, err := bc.backupStore.GetBackupCodes(userid)
	if err != nil {
		log.Printf("BackupController.IsSupported error fetching codes (%s)", err)
		return false
	}

	// Check for an active code
	// TODO: this means that when all active codes have been used this method will be disabled
	// is this the desired behaviour? Could we warn a user prior to disabling it?
	available := false
	for _, c := range codes {
		code := c.(Code)
		if !code.IsUsed() {
			available = true
		}
	}

	return available
}

// ValidateName validates a code name
// This is intended to be checked periodically when using other login mechanisms
// to ensure user still has access to recovery codes
func (bc *Controller) ValidateName(userid string, name string) (bool, error) {
	// Fetch associated codes with for the provided user
	codes, err := bc.backupStore.GetBackupCodes(userid)
	if err != nil {
		log.Println(err)
		return false, err
	}

	// Check provided code against enabled codes
	for _, c := range codes {
		code := c.(Code)
		if (code.GetName() == name) && !code.IsUsed() {
			return true, nil
		}
	}

	// No matching code found
	return false, nil
}

// ValidateCode validates a backup code use and marks the code as used
func (bc *Controller) ValidateCode(userid string, codeString string) (bool, error) {

	// Split codeString into words
	phrase := strings.Split(codeString, " ")

	// Fetch key name
	name := strings.Join(phrase[:recoveryNameLen], " ")

	// Translate mnemonic form to bytes
	mnemonicKey := strings.Join(phrase[recoveryNameLen:], " ")
	key, err := mnemonics.FromString(mnemonicKey, mnemonics.English)

	// Fetch associated codes with for the provided user
	c, err := bc.backupStore.GetBackupCodeByName(userid, name)
	if err != nil {
		log.Println(err)
		return false, err
	}
	if c == nil {
		return false, nil
	}
	code := c.(Code)

	// Check code matches
	if (code.GetName() != name) || code.IsUsed() {
		return false, nil
	}

	// Check provided code against stored hash
	err = bcrypt.CompareHashAndPassword([]byte(code.GetHashedSecret()), key)
	if err != nil {
		return false, nil
	}

	// Mark code as disabled
	code.SetUsed()

	// Update code in database
	_, err = bc.backupStore.UpdateBackupCode(code)
	if err != nil {
		log.Println(err)
		return false, err
	}

	return true, nil
}
