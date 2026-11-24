// internal/repository/telegram_account_repository.go
package repository

import (
	"errors"
	"fmt"

	"github.com/azharf99/tele-gateway/internal/domain"
	"gorm.io/gorm"
)

type telegramAccountRepository struct {
	db  *gorm.DB
	box *SecretBox
}

func NewTelegramAccountRepository(db *gorm.DB, encryptionSecret string) domain.TelegramAccountRepository {
	return &telegramAccountRepository{
		db:  db,
		box: NewSecretBox(encryptionSecret),
	}
}

func (r *telegramAccountRepository) encryptSecrets(acc *domain.TelegramAccount) (string, string, error) {
	origHash, origPass := acc.AppHash, acc.TwoFAPassword
	encHash, err := r.box.Encrypt(origHash)
	if err != nil {
		return "", "", fmt.Errorf("encrypt app_hash: %w", err)
	}
	encPass, err := r.box.Encrypt(origPass)
	if err != nil {
		return "", "", fmt.Errorf("encrypt two_fa_password: %w", err)
	}
	acc.AppHash = encHash
	acc.TwoFAPassword = encPass
	return origHash, origPass, nil
}

func (r *telegramAccountRepository) decryptAccount(acc *domain.TelegramAccount) error {
	if acc == nil {
		return nil
	}
	if acc.AppHash != "" {
		plain, err := r.box.Decrypt(acc.AppHash)
		if err != nil {
			return fmt.Errorf("decrypt app_hash for account %d: %w", acc.ID, err)
		}
		acc.AppHash = plain
	}
	if acc.TwoFAPassword != "" {
		plain, err := r.box.Decrypt(acc.TwoFAPassword)
		if err != nil {
			return fmt.Errorf("decrypt two_fa_password for account %d: %w", acc.ID, err)
		}
		acc.TwoFAPassword = plain
	}
	acc.HasCustomAppHash = acc.AppHash != ""
	acc.HasTwoFAPassword = acc.TwoFAPassword != ""
	return nil
}

func (r *telegramAccountRepository) Create(account *domain.TelegramAccount) error {
	origHash, origPass, err := r.encryptSecrets(account)
	if err != nil {
		return err
	}
	defer func() {
		account.AppHash = origHash
		account.TwoFAPassword = origPass
		account.HasCustomAppHash = origHash != ""
		account.HasTwoFAPassword = origPass != ""
	}()

	// Recover soft-deleted record with same phone number if any
	var existing domain.TelegramAccount
	lookupErr := r.db.Unscoped().Where("phone_number = ?", account.PhoneNumber).First(&existing).Error
	if lookupErr == nil && existing.DeletedAt.Valid {
		account.ID = existing.ID
		account.CreatedAt = existing.CreatedAt
		return r.db.Unscoped().Model(account).Select("*").Updates(account).Update("deleted_at", nil).Error
	}
	return r.db.Create(account).Error
}

func (r *telegramAccountRepository) Update(account *domain.TelegramAccount) error {
	origHash, origPass, err := r.encryptSecrets(account)
	if err != nil {
		return err
	}
	defer func() {
		account.AppHash = origHash
		account.TwoFAPassword = origPass
		account.HasCustomAppHash = origHash != ""
		account.HasTwoFAPassword = origPass != ""
	}()
	return r.db.Save(account).Error
}

func (r *telegramAccountRepository) Delete(id uint) error {
	return r.db.Unscoped().Delete(&domain.TelegramAccount{}, id).Error
}

func (r *telegramAccountRepository) FindByID(id uint) (*domain.TelegramAccount, error) {
	var acc domain.TelegramAccount
	if err := r.db.First(&acc, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}
	if err := r.decryptAccount(&acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *telegramAccountRepository) FindByPhone(phone string) (*domain.TelegramAccount, error) {
	var acc domain.TelegramAccount
	if err := r.db.Where("phone_number = ?", phone).First(&acc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}
	if err := r.decryptAccount(&acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *telegramAccountRepository) FindAll() ([]domain.TelegramAccount, error) {
	var accounts []domain.TelegramAccount
	if err := r.db.Order("id asc").Find(&accounts).Error; err != nil {
		return nil, err
	}
	for i := range accounts {
		if err := r.decryptAccount(&accounts[i]); err != nil {
			return nil, err
		}
	}
	return accounts, nil
}

func (r *telegramAccountRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&domain.TelegramAccount{}).Count(&count).Error
	return count, err
}

func (r *telegramAccountRepository) SetDefaultB2B(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.TelegramAccount{}).Where("id <> ?", id).Update("is_default_b2b", false).Error; err != nil {
			return err
		}
		return tx.Model(&domain.TelegramAccount{}).Where("id = ?", id).Updates(map[string]any{
			"enable_b2b":     true,
			"is_default_b2b": true,
		}).Error
	})
}

func (r *telegramAccountRepository) ClearDefaultB2BExcept(id uint) error {
	return r.db.Model(&domain.TelegramAccount{}).Where("id <> ?", id).Update("is_default_b2b", false).Error
}
