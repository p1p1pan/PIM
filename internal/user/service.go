package user

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	authsvc "pim/internal/auth"
)

// Service 封装用户域业务：注册、登录（含密码校验与 JWT 签发）、按 ID 查用户。由 handler 或 auth-service 调用。
type Service struct {
	db *gorm.DB
}

// NewService 根据传入的 db 构造 Service，供 handler 或 cmd 使用。
func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Register 检查用户名是否已存在，用 bcrypt 哈希密码后写入 users 表，返回用户信息（不含密码）。
func (s *Service) Register(username, password string) (*User, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password are required")
	}

	var count int64
	if err := s.db.Model(&User{}).Where("username = ?", username).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("username already exists")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	u := &User{
		Username: username,
		Password: string(hashed),
	}
	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}
	u.Password = ""
	return u, nil
}

// Login 按用户名查库、用 bcrypt.CompareHashAndPassword 校验密码，通过后调用 auth.GenerateToken 签发 JWT，返回用户与 token。
func (s *Service) Login(username, password string) (*User, string, error) {
	if username == "" || password == "" {
		return nil, "", errors.New("username and password are required")
	}

	var u User
	if err := s.db.Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("user not found")
		}
		return nil, "", err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return nil, "", errors.New("invalid password")
	}

	tokenString, err := authsvc.GenerateToken(u.ID, u.Username, 24*time.Hour)
	if err != nil {
		return nil, "", err
	}

	u.Password = ""
	return &u, tokenString, nil
}

// GetByID 按主键查 users 表，返回用户信息并清空 Password 字段再返回。
func (s *Service) GetByID(id uint) (*User, error) {
	var u User
	if err := s.db.First(&u, id).Error; err != nil {
		return nil, err
	}
	u.Password = ""
	return &u, nil
}

