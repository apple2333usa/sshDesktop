<script setup lang="ts">
import { computed } from 'vue'
import { NConfigProvider, NDialogProvider, NMessageProvider, NNotificationProvider, darkTheme, zhCN, dateZhCN } from 'naive-ui'
import { useRouter } from 'vue-router'
import { setUnauthorizedHandler } from '@/api/http'
import { usePrefs } from '@/stores/prefs'
import { useSession } from '@/stores/session'
import { themeOverrides } from '@/theme/naive'
import DangerDialog from '@/components/DangerDialog.vue'

const prefs = usePrefs()
const session = useSession()
const router = useRouter()

const theme = computed(() => (prefs.dark ? darkTheme : null))
const overrides = computed(() => themeOverrides(prefs.dark))

// 会话过期：回到登录页
setUnauthorizedHandler(() => {
  if (!session.me) return
  session.clear()
  router.replace({ name: 'login', query: { expired: '1' } })
})
</script>

<template>
  <NConfigProvider :theme="theme" :theme-overrides="overrides" :locale="zhCN" :date-locale="dateZhCN" abstract>
    <NMessageProvider placement="top">
      <NNotificationProvider placement="bottom-right">
        <NDialogProvider>
          <RouterView />
          <DangerDialog />
        </NDialogProvider>
      </NNotificationProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>
