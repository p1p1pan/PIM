package service

import (
	"errors"
	"time"

	authsvc "pim/internal/auth/service"
	"pim/internal/user/model"
	"pim/internal/user/repo"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	repo *repo.UserRepo
}

// NewService 创建用户业务服务。
func NewService(r *repo.UserRepo) *Service {
	return &Service{repo: r}
}

// Register 注册用户并完成密码哈希。
func (s *Service) Register(username, password string) (*model.User, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password are required")
	}
	count, err := s.repo.CountByUsername(username)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("username already exists")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &model.User{Username: username, Password: string(hashed)}
	if err := s.repo.Create(u); err != nil {
		return nil, err
	}
	u.Password = ""
	return u, nil
}

// Login 校验密码并签发 token。
func (s *Service) Login(username, password string) (*model.User, string, error) {
	if username == "" || password == "" {
		return nil, "", errors.New("username and password are required")
	}
	u, err := s.repo.GetByUsername(username)
	if err != nil {
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
	return u, tokenString, nil
}

// GetByID 查询用户并过滤敏感字段。
func (s *Service) GetByID(id uint) (*model.User, error) {
	u, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	u.Password = ""
	return u, nil
}
