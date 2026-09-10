package model

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
)

func TestUserStatusAndRoleConstantsFitColumns(t *testing.T) {
	statusSize := gormSize(t, "Status")
	roleSize := gormSize(t, "Role")
	if statusSize != StatusColumnSize {
		t.Fatalf("Status gorm size = %d, want %d", statusSize, StatusColumnSize)
	}
	if roleSize != RoleColumnSize {
		t.Fatalf("Role gorm size = %d, want %d", roleSize, RoleColumnSize)
	}
	for _, status := range []string{domain.StatusActive, domain.StatusPending, domain.StatusDisabled} {
		if len(status) > statusSize {
			t.Fatalf("status %q length %d exceeds column size %d", status, len(status), statusSize)
		}
	}
	for _, role := range []string{domain.RoleAdmin, domain.RoleUser} {
		if len(role) > roleSize {
			t.Fatalf("role %q length %d exceeds column size %d", role, len(role), roleSize)
		}
	}
}

func gormSize(t *testing.T, field string) int {
	t.Helper()
	sf, ok := reflect.TypeOf(User{}).FieldByName(field)
	if !ok {
		t.Fatalf("missing field %s", field)
	}
	for _, part := range strings.Split(sf.Tag.Get("gorm"), ";") {
		key, value, found := strings.Cut(part, ":")
		if !found || key != "size" {
			continue
		}
		size, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("parse gorm size for %s: %v", field, err)
		}
		return size
	}
	t.Fatalf("missing gorm size for %s", field)
	return 0
}
