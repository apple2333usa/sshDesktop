<script setup lang="ts">
// 账号与权限（仅管理员）：账号、角色与权限点、在线会话、被封禁的 IP。
import { computed, h, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCheckbox, NCheckboxGroup, NDataTable, NForm, NFormItem, NInput, NModal, NSelect, NSpace, NSwitch, NTabPane, NTabs,
  NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { BanRow, PermInfo, RoleRow, SessionRow, UserRow } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { withDanger } from '@/components/danger'
import { errorText, formatTime } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()

const tab = ref<string>((host.props.tab as string) || 'users')
watch(tab, (v) => host.setState({ tab: v }))
host.setTitle(t('apps.users'))

const users = ref<UserRow[]>([])
const roles = ref<RoleRow[]>([])
const sessions = ref<SessionRow[]>([])
const bans = ref<BanRow[]>([])
const catalog = ref<PermInfo[]>([])
const loading = ref(false)

const fail = (e: unknown) => message.error(errorText(e))

async function load() {
  loading.value = true
  try {
    const [u, r, s, b, c] = await Promise.all([
      api.get<UserRow[]>('/users'),
      api.get<RoleRow[]>('/roles'),
      api.get<SessionRow[]>('/sessions'),
      api.get<BanRow[]>('/bans'),
      catalog.value.length ? Promise.resolve(catalog.value) : api.get<PermInfo[]>('/permissions'),
    ])
    users.value = u
    roles.value = r
    sessions.value = s
    bans.value = b
    catalog.value = c
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const roleOptions = computed(() => roles.value.map((r) => ({ label: r.name, value: r.id })))

function randomPassword() {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'
  const buf = new Uint32Array(16)
  crypto.getRandomValues(buf)
  return Array.from(buf, (x) => chars[x % chars.length]).join('')
}

/* ---------- 账号 ---------- */
const userForm = reactive({ show: false, id: 0, username: '', password: '', roleId: null as number | null, saving: false })

function addUser() {
  Object.assign(userForm, { show: true, id: 0, username: '', password: randomPassword(), roleId: roles.value.find((r) => !r.isAdmin)?.id ?? null })
}

async function saveUser() {
  userForm.saving = true
  try {
    if (userForm.id) {
      await api.post(`/users/${userForm.id}/password`, { password: userForm.password })
      message.success(t('users.passwordReset'))
    } else {
      await api.post('/users', { username: userForm.username, password: userForm.password, roleId: userForm.roleId })
      message.success(t('users.created'))
    }
    userForm.show = false
    load()
  } catch (e) {
    fail(e)
  } finally {
    userForm.saving = false
  }
}

function resetPassword(u: UserRow) {
  Object.assign(userForm, { show: true, id: u.id, username: u.username, password: randomPassword(), roleId: u.roleId })
}

async function updateUser(u: UserRow, patch: { roleId?: number; disabled?: boolean }) {
  try {
    await api.put(`/users/${u.id}`, patch)
  } catch (e) {
    fail(e)
  }
  load()
}

async function deleteUser(u: UserRow) {
  try {
    const r = await withDanger(
      { level: 1, action: t('users.deleteAction'), impact: u.username, confirmText: t('users.deleteConfirm', { name: u.username }) },
      () => api.del(`/users/${u.id}`),
    )
    if (r !== undefined) message.success(t('users.deleted'))
  } catch (e) {
    fail(e)
  }
  load()
}

const userColumns = computed<DataTableColumns<UserRow>>(() => [
  {
    title: t('users.username'),
    key: 'username',
    render: (u) =>
      h('span', [u.username, u.id === session.me?.user.id ? h(NTag, { size: 'tiny', bordered: false, style: 'margin-left:6px' }, () => t('users.you')) : null]),
  },
  {
    title: t('users.role'),
    key: 'roleId',
    width: 160,
    render: (u) =>
      h(NSelect, {
        size: 'small',
        value: u.roleId,
        options: roleOptions.value,
        'onUpdate:value': (v: number) => updateUser(u, { roleId: v }),
      }),
  },
  {
    title: t('users.enabled'),
    key: 'disabled',
    width: 80,
    render: (u) =>
      h(NSwitch, {
        size: 'small',
        value: !u.disabled,
        disabled: u.id === session.me?.user.id,
        'onUpdate:value': (v: boolean) => updateUser(u, { disabled: !v }),
      }),
  },
  { title: t('users.lastLogin'), key: 'lastLoginAt', width: 150, render: (u) => formatTime(u.lastLoginAt) },
  { title: t('users.lastIp'), key: 'lastLoginIp', width: 140, render: (u) => u.lastLoginIp || '-' },
  { title: t('users.createdAt'), key: 'createdAt', width: 150, render: (u) => formatTime(u.createdAt) },
  {
    title: '',
    key: 'ops',
    width: 170,
    render: (u) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', secondary: true, onClick: () => resetPassword(u) }, () => t('users.resetPassword')),
        h(NButton, { size: 'tiny', secondary: true, type: 'error', disabled: u.id === session.me?.user.id, onClick: () => deleteUser(u) }, () => t('common.delete')),
      ]),
  },
])

/* ---------- 角色 ---------- */
const roleForm = reactive({ show: false, id: 0, name: '', description: '', permissions: [] as string[], isAdmin: false, saving: false })

function editRole(r?: RoleRow) {
  Object.assign(roleForm, r ? { show: true, id: r.id, name: r.name, description: r.description, permissions: [...r.permissions], isAdmin: r.isAdmin } : { show: true, id: 0, name: '', description: '', permissions: [], isAdmin: false })
}

async function saveRole() {
  roleForm.saving = true
  const body = { name: roleForm.name, description: roleForm.description, permissions: roleForm.permissions }
  try {
    if (roleForm.id) await api.put(`/roles/${roleForm.id}`, body)
    else await api.post('/roles', body)
    roleForm.show = false
    message.success(t('common.saved'))
    load()
  } catch (e) {
    fail(e)
  } finally {
    roleForm.saving = false
  }
}

async function deleteRole(r: RoleRow) {
  try {
    const res = await withDanger(
      { level: 1, action: t('users.deleteRoleAction'), impact: r.name, confirmText: t('users.deleteConfirm', { name: r.name }) },
      () => api.del(`/roles/${r.id}`),
    )
    if (res !== undefined) message.success(t('users.deleted'))
  } catch (e) {
    fail(e)
  }
  load()
}

const grantable = computed(() => catalog.value.filter((p) => !p.adminOnly))

const roleColumns = computed<DataTableColumns<RoleRow>>(() => [
  { title: t('users.roleName'), key: 'name', width: 140, render: (r) => h('span', [r.name, r.isAdmin ? h(NTag, { size: 'tiny', type: 'warning', bordered: false, style: 'margin-left:6px' }, () => t('users.admin')) : null]) },
  { title: t('users.description'), key: 'description', ellipsis: { tooltip: true } },
  {
    title: t('users.permissions'),
    key: 'permissions',
    render: (r) =>
      r.isAdmin
        ? h('span', { class: 'sd-muted' }, t('users.allPermissions'))
        : h(NSpace, { size: 4 }, () => r.permissions.map((p) => h(NTag, { size: 'small', bordered: false }, () => t('perms.' + p.replace(':', '_'))))),
  },
  { title: t('users.userCount'), key: 'userCount', width: 70 },
  {
    title: '',
    key: 'ops',
    width: 130,
    render: (r) =>
      r.isAdmin
        ? null
        : h(NSpace, { size: 4 }, () => [
            h(NButton, { size: 'tiny', secondary: true, onClick: () => editRole(r) }, () => t('common.edit')),
            h(NButton, { size: 'tiny', secondary: true, type: 'error', disabled: r.userCount > 0, onClick: () => deleteRole(r) }, () => t('common.delete')),
          ]),
  },
])

/* ---------- 会话与封禁 ---------- */
async function kick(s: SessionRow) {
  try {
    await api.del(`/sessions/${s.id}`)
    message.success(t('users.kicked'))
  } catch (e) {
    fail(e)
  }
  load()
}

async function unban(b: BanRow) {
  try {
    await api.del(`/bans/${encodeURIComponent(b.ip)}`)
    message.success(t('users.unbanned'))
  } catch (e) {
    fail(e)
  }
  load()
}

const sessionColumns = computed<DataTableColumns<SessionRow>>(() => [
  { title: t('users.username'), key: 'username', width: 120, render: (s) => h('span', [s.username, s.current ? h(NTag, { size: 'tiny', type: 'success', bordered: false, style: 'margin-left:6px' }, () => t('users.current')) : null]) },
  { title: 'IP', key: 'ip', width: 140 },
  { title: t('users.client'), key: 'userAgent', ellipsis: { tooltip: true } },
  { title: t('users.loginAt'), key: 'createdAt', width: 150, render: (s) => formatTime(s.createdAt) },
  { title: t('users.lastSeen'), key: 'lastSeenAt', width: 150, render: (s) => formatTime(s.lastSeenAt) },
  {
    title: '',
    key: 'ops',
    width: 90,
    render: (s) => (s.current ? null : h(NButton, { size: 'tiny', secondary: true, type: 'error', onClick: () => kick(s) }, () => t('users.kick'))),
  },
])

const banColumns = computed<DataTableColumns<BanRow>>(() => [
  { title: 'IP', key: 'ip', width: 160 },
  { title: t('users.reason'), key: 'reason', ellipsis: { tooltip: true } },
  { title: t('users.bannedAt'), key: 'createdAt', width: 150, render: (b) => formatTime(b.createdAt) },
  { title: t('users.until'), key: 'until', width: 150, render: (b) => formatTime(b.until) },
  { title: '', key: 'ops', width: 90, render: (b) => h(NButton, { size: 'tiny', secondary: true, onClick: () => unban(b) }, () => t('users.unban')) },
])

function copy(text: string) {
  navigator.clipboard?.writeText(text).then(() => message.success(t('common.copied')))
}
</script>

<template>
  <div class="sd-page users" data-testid="users">
    <NTabs v-model:value="tab" type="line" class="tabs" size="small">
      <NTabPane name="users" :tab="t('users.accounts')">
        <div class="sd-toolbar">
          <NButton size="small" type="primary" data-testid="user-add" @click="addUser">＋ {{ t('users.addUser') }}</NButton>
          <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="userColumns" :data="users" :row-key="(u: UserRow) => u.id" :bordered="false" :scroll-x="900" />
      </NTabPane>
      <NTabPane name="roles" :tab="t('users.roles')">
        <div class="sd-toolbar">
          <NButton size="small" type="primary" @click="editRole()">＋ {{ t('users.addRole') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="roleColumns" :data="roles" :row-key="(r: RoleRow) => r.id" :bordered="false" />
      </NTabPane>
      <NTabPane name="sessions" :tab="t('users.sessions') + ` (${sessions.length})`">
        <div class="sd-toolbar">
          <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="sessionColumns" :data="sessions" :row-key="(s: SessionRow) => s.id" :bordered="false" :scroll-x="800" />
      </NTabPane>
      <NTabPane name="bans" :tab="t('users.bans') + ` (${bans.length})`">
        <div class="sd-toolbar">
          <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
          <span class="sd-muted hint">{{ t('users.banHint') }}</span>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="banColumns" :data="bans" :row-key="(b: BanRow) => b.ip" :bordered="false" />
      </NTabPane>
    </NTabs>

    <NModal v-model:show="userForm.show" preset="card" :title="userForm.id ? t('users.resetPasswordFor', { name: userForm.username }) : t('users.addUser')" style="width: 440px">
      <NForm label-placement="left" label-width="70" @submit.prevent="saveUser">
        <NFormItem v-if="!userForm.id" :label="t('users.username')">
          <NInput v-model:value="userForm.username" data-testid="user-form-name" :placeholder="t('users.usernameRule')" />
        </NFormItem>
        <NFormItem :label="t('users.password')">
          <NInput v-model:value="userForm.password" class="sd-mono" data-testid="user-form-password" />
          <NButton quaternary size="small" @click="userForm.password = randomPassword()">{{ t('users.generate') }}</NButton>
          <NButton quaternary size="small" @click="copy(userForm.password)">{{ t('common.copy') }}</NButton>
        </NFormItem>
        <NFormItem v-if="!userForm.id" :label="t('users.role')">
          <NSelect v-model:value="userForm.roleId" :options="roleOptions" />
        </NFormItem>
        <div class="sd-muted hint">{{ userForm.id ? t('users.resetHint') : t('users.passwordHint') }}</div>
      </NForm>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="userForm.show = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="userForm.saving" data-testid="user-form-save" @click="saveUser">{{ t('common.ok') }}</NButton>
        </NSpace>
      </template>
    </NModal>

    <NModal v-model:show="roleForm.show" preset="card" :title="roleForm.id ? t('users.editRole') : t('users.addRole')" style="width: 560px">
      <NForm label-placement="left" label-width="70">
        <NFormItem :label="t('users.roleName')"><NInput v-model:value="roleForm.name" /></NFormItem>
        <NFormItem :label="t('users.description')"><NInput v-model:value="roleForm.description" /></NFormItem>
        <NFormItem :label="t('users.permissions')">
          <NCheckboxGroup v-model:value="roleForm.permissions">
            <div class="perms">
              <div v-for="p in grantable" :key="p.key" class="perm">
                <NCheckbox :value="p.key" :label="t('perms.' + p.key.replace(':', '_'))" />
                <NTag v-if="p.rootEquivalent" size="tiny" type="error" :bordered="false">{{ t('users.rootEquivalent') }}</NTag>
                <span class="sd-muted sd-mono key">{{ p.key }}</span>
              </div>
            </div>
          </NCheckboxGroup>
        </NFormItem>
        <div class="sd-muted hint">{{ t('users.rootHint') }}</div>
      </NForm>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="roleForm.show = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="roleForm.saving" @click="saveRole">{{ t('common.save') }}</NButton>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.users {
  background: var(--sd-surface);
  padding: 0 12px;
}
.tabs {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.tabs :deep(.n-tabs-pane-wrapper) {
  flex: 1;
  min-height: 0;
}
.tabs :deep(.n-tab-pane) {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.hint {
  font-size: 12px;
}
.sd-toolbar .hint {
  margin-left: auto;
}
.perms {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.perm {
  display: flex;
  align-items: center;
  gap: 8px;
}
.key {
  font-size: 11px;
}
</style>
