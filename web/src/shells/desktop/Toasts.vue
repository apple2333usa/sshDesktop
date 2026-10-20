<script setup lang="ts">
// 右下角弹出的提示（面板开着时）。
import { useNotify } from '@/stores/notify'

const notify = useNotify()
const icon = { success: '✓', error: '!', warning: '!', info: 'i' }
</script>

<template>
  <div class="toasts">
    <TransitionGroup name="toast">
      <div v-for="n in notify.toasts" :key="n.id" class="toast" :class="n.type" data-testid="toast" @click="notify.dismissToast(n.id)">
        <span class="ic">{{ icon[n.type] }}</span>
        <div class="txt">
          <div class="title">{{ n.title }}</div>
          <div v-if="n.content" class="content">{{ n.content }}</div>
        </div>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toasts {
  position: absolute;
  right: 12px;
  bottom: 60px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  z-index: 100002;
  pointer-events: none;
}
.toast {
  pointer-events: auto;
  width: 340px;
  max-width: calc(100vw - 24px);
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 12px 14px;
  border-radius: 8px;
  background: var(--sd-acrylic);
  backdrop-filter: blur(40px) saturate(150%);
  border: 1px solid var(--sd-border-strong);
  box-shadow: var(--sd-shadow);
}
.ic {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  background: var(--sd-accent);
}
.success .ic {
  background: #0f7b0f;
}
.error .ic {
  background: #c42b1c;
}
.warning .ic {
  background: #9d5d00;
}
.title {
  font-weight: 600;
  font-size: 13px;
}
.content {
  font-size: 12px;
  color: var(--sd-text-2);
  margin-top: 2px;
  word-break: break-all;
}
.toast-enter-active,
.toast-leave-active {
  transition: all 0.2s;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(30px);
}
</style>
