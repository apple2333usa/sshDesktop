<script setup lang="ts">
// 渲染彩色 SVG 图标。每个实例给渐变 id 加唯一后缀，避免隐藏窗口里的同名 id 导致图标丢色。
import { computed } from 'vue'
import { iconSVG } from './icons'

const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: 32 })

const suffix = '_' + Math.random().toString(36).slice(2, 9)

const html = computed(() =>
  iconSVG(props.name)
    .replace(/id="([\w-]+)"/g, `id="$1${suffix}"`)
    .replace(/url\(#([\w-]+)\)/g, `url(#$1${suffix})`),
)
</script>

<template>
  <span class="sd-icon" :style="{ width: size + 'px', height: size + 'px' }" v-html="html" />
</template>

<style scoped>
.sd-icon {
  display: inline-flex;
  flex-shrink: 0;
}
.sd-icon :deep(svg) {
  width: 100%;
  height: 100%;
}
</style>
