// Package auth contains provider-neutral identity and authorisation primitives.
package auth

import "sort"

type Role string

const (
	Agent         Role = "agent"
	Supervisor    Role = "supervisor"
	Administrator Role = "administrator"
)

type Status string

const (
	Active   Status = "active"
	Disabled Status = "disabled"
)

type Permission string

const (
	PermissionConversationRead     Permission = "conversation.read"
	PermissionConversationReply    Permission = "conversation.reply"
	PermissionConversationReassign Permission = "conversation.reassign"
	PermissionAnalyticsRead        Permission = "analytics.read"
	PermissionChannelManage        Permission = "channel.manage"
	PermissionWorkflowManage       Permission = "workflow.manage"
	PermissionUserManage           Permission = "user.manage"
	PermissionRoleManage           Permission = "role.manage"
	PermissionQueueManage          Permission = "queue.manage"
	PermissionAuditRead            Permission = "audit.read"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	Agent: {
		PermissionConversationRead: {}, PermissionConversationReply: {},
	},
	Supervisor: {
		PermissionConversationRead: {}, PermissionConversationReply: {}, PermissionConversationReassign: {},
		PermissionAnalyticsRead: {}, PermissionAuditRead: {},
	},
	Administrator: {
		PermissionConversationRead: {}, PermissionConversationReply: {}, PermissionConversationReassign: {},
		PermissionAnalyticsRead: {}, PermissionAuditRead: {}, PermissionChannelManage: {}, PermissionWorkflowManage: {},
		PermissionUserManage: {}, PermissionRoleManage: {}, PermissionQueueManage: {},
	},
}

func ValidRole(role Role) bool       { _, ok := rolePermissions[role]; return ok }
func ValidStatus(status Status) bool { return status == Active || status == Disabled }
func ValidPermission(permission Permission) bool {
	for _, permissions := range rolePermissions {
		if _, ok := permissions[permission]; ok {
			return true
		}
	}
	return false
}
func Allowed(role Role, permission Permission) bool {
	permissions, ok := rolePermissions[role]
	if !ok || !ValidPermission(permission) {
		return false
	}
	_, ok = permissions[permission]
	return ok
}
func RolePermissions(role Role) []Permission {
	permissions := rolePermissions[role]
	result := make([]Permission, 0, len(permissions))
	for permission := range permissions {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

type Principal struct {
	UserID string
	Login  string
	Email  string
	Name   string
	Role   Role
}
