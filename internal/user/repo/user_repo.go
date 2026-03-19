package repo

import (
	"pim/internal/user/model"

	"gorm.io/gorm"
)

type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo 创建用户仓储。
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

// CountByUsername 统计指定用户名数量，用于注册前唯一性检查。
func (r *UserRepo) CountByUsername(username string) (int64, error) {
	var count int64
	err := r.db.Model(&model.User{}).Where("username = ?", username).Count(&count).Error
	return count, err
}

// Create 新建用户记录。
func (r *UserRepo) Create(u *model.User) error {
	return r.db.Create(u).Error
}

// GetByUsername 根据用户名查询用户。
func (r *UserRepo) GetByUsername(username string) (*model.User, error) {
	var u model.User
	if err := r.db.Where("username = ?", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByID 根据主键查询用户。
func (r *UserRepo) GetByID(id uint) (*model.User, error) {
	var u model.User
	if err := r.db.First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}
