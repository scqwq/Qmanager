//go:build !windows

package cloud123

import "errors"

func protect([]byte) ([]byte, error)         { return nil, errors.New("加密记住登录仅支持Windows") }
func unprotect([]byte) ([]byte, error)       { return nil, errors.New("加密记住登录仅支持Windows") }
func replaceCredential(string, string) error { return errors.New("当前平台不支持凭据保存") }
