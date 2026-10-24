//go:build windows

package host

import "testing"

// TestWindowsCredentialIsolationAndDeletion 使用随机测试键验证系统凭据往返、来源隔离及删除，收尾始终删除测试凭据。
func TestWindowsCredentialIsolationAndDeletion(t *testing.T) {
	d, ctx := desktopFixture(t)
	id, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	key := "test-" + id
	defer func() { _, _ = d.Credential(ctx, key, "", "delete") }()
	if _, err = d.Credential(ctx, key, "测试秘密-123", "set"); err != nil {
		t.Fatal(err)
	}
	value, err := d.Credential(ctx, key, "", "get")
	if err != nil || value != "测试秘密-123" {
		t.Fatal("credential round trip failed")
	}
	other, otherCtx := desktopFixture(t)
	if _, err = other.Credential(otherCtx, key, "", "get"); err == nil {
		t.Fatal("credential leaked across data directories")
	}
	if _, err = d.Credential(ctx, key, "", "delete"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Credential(ctx, key, "", "get"); err == nil {
		t.Fatal("deleted credential was readable")
	}
}
