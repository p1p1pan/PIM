package model

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// EnsureGroupMessageMentionMetaColumn 在 AutoMigrate 之前执行，兼容「已有群消息行但尚无 mention_meta」的库：
// PostgreSQL 不允许对非空表直接 ADD NOT NULL 且无 DEFAULT，否则会失败（SQLSTATE 23502）。
func EnsureGroupMessageMentionMetaColumn(db *gorm.DB) error {
	if !db.Migrator().HasTable(&GroupMessage{}) {
		return nil
	}
	empty := EmptyMentionMetaJSON
	q := "'" + strings.ReplaceAll(empty, "'", "''") + "'"
	if !db.Migrator().HasColumn(&GroupMessage{}, "MentionMeta") {
		sql := fmt.Sprintf(
			`ALTER TABLE group_messages ADD COLUMN mention_meta text NOT NULL DEFAULT %s`,
			q,
		)
		return db.Exec(sql).Error
	}

	// 先 COUNT：无脏数据则跳过全表 UPDATE（TRIM 全表扫描是启动慢 SQL 的主因）。
	var bad int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM group_messages
		WHERE mention_meta IS NULL OR mention_meta = ''
	`).Scan(&bad).Error; err != nil {
		return err
	}
	if bad > 0 {
		if err := db.Exec(
			`UPDATE group_messages SET mention_meta = ? WHERE mention_meta IS NULL OR mention_meta = ''`,
			empty,
		).Error; err != nil {
			return err
		}
	}

	if err := db.Exec(fmt.Sprintf(
		`ALTER TABLE group_messages ALTER COLUMN mention_meta SET DEFAULT %s`,
		q,
	)).Error; err != nil {
		return err
	}
	return db.Exec(`ALTER TABLE group_messages ALTER COLUMN mention_meta SET NOT NULL`).Error
}
