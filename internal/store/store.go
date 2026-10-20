// Package store 负责数据库连接、迁移与键值设置。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	DB *gorm.DB

	mu       sync.RWMutex
	settings map[string]string
}

// Open 打开（必要时创建）数据库并执行迁移。迁移只增不减，保证可回滚到上一版本。
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := gorm.Open(gormlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		// SQLite 写操作串行，单连接最省心；读写量对 5 台规模绰绰有余。
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(allModels()...); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	if path != ":memory:" {
		os.Chmod(path, 0o600)
	}
	s := &Store{DB: db, settings: map[string]string{}}
	var rows []Setting
	db.Find(&rows)
	for _, r := range rows {
		s.settings[r.Key] = r.Value
	}
	return s, nil
}

func (s *Store) Close() error {
	sqlDB, err := s.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// GetSetting 读取设置并解码到 out；不存在时 out 保持原值（即默认值）。
func (s *Store) GetSetting(key string, out any) {
	s.mu.RLock()
	v, ok := s.settings[key]
	s.mu.RUnlock()
	if ok {
		json.Unmarshal([]byte(v), out)
	}
}

func (s *Store) SetSetting(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.DB.Save(&Setting{Key: key, Value: string(b)}).Error; err != nil {
		return err
	}
	s.mu.Lock()
	s.settings[key] = string(b)
	s.mu.Unlock()
	return nil
}
