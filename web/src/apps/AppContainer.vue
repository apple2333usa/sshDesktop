<script setup lang="ts">
// 加载应用组件并注入 AppHost。窗口和后台页面都通过它承载应用。
import { defineAsyncComponent, provide, toRef, type Component } from 'vue'
import { NSpin } from 'naive-ui'
import { appMap } from './registry'
import { AppHostKey, type AppHost } from './host'

const props = defineProps<{
  appId: string
  appProps: Record<string, any>
  mode: 'desktop' | 'admin'
  windowId: string | null
  active: boolean
  visible: boolean
}>()

const emit = defineEmits<{
  title: [string]
  close: []
  open: [string, Record<string, any> | undefined]
  guard: [() => boolean | Promise<boolean>]
}>()

const app = appMap[props.appId]
const Comp = defineAsyncComponent({
  loader: app.component as () => Promise<Component>,
  loadingComponent: { render: () => null },
  delay: 0,
})

const host: AppHost = {
  mode: props.mode,
  windowId: props.windowId,
  props: props.appProps,
  active: toRef(props, 'active'),
  visible: toRef(props, 'visible'),
  setTitle: (t) => emit('title', t),
  setState: (patch) => Object.assign(props.appProps, patch),
  close: () => emit('close'),
  openApp: (id, p) => emit('open', id, p),
  onBeforeClose: (fn) => emit('guard', fn),
}
provide(AppHostKey, host)
</script>

<template>
  <Suspense>
    <Comp />
    <template #fallback>
      <div class="loading"><NSpin size="small" /></div>
    </template>
  </Suspense>
</template>

<style scoped>
.loading {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
