<script setup lang="ts">
// 切换面板访问方式后，浏览器带着一次性令牌打开新地址（?confirm-access=…）。在这里提交确认，
// 新地址能打开就说明切换成功，不必先登录。令牌在 main.ts 启动时从地址栏取出，避免进入登录跳转参数。
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDialog } from 'naive-ui'
import { api } from '@/api/http'
import { errorText } from '@/i18n'

const props = defineProps<{ token: string }>()
const { t } = useI18n()
const dialog = useDialog()

onMounted(async () => {
  if (!props.token) return
  try {
    await api.post('/panel/access/confirm-token', { token: props.token })
    dialog.success({ title: t('access.confirmedTitle'), content: t('access.confirmedText', { url: location.origin }), positiveText: t('common.ok') })
  } catch (e) {
    dialog.error({ title: t('access.confirmFailed'), content: errorText(e), positiveText: t('common.ok') })
  }
})
</script>

<template>
  <span v-if="false" />
</template>
