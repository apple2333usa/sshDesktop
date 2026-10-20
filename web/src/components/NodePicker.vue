<script setup lang="ts">
// 节点选择器：本机和各远程服务器，带在线状态圆点。只有本机时不显示。
import { computed, h } from 'vue'
import { NSelect, type SelectOption } from 'naive-ui'
import { useNodes } from '@/stores/nodes'

const props = withDefaults(defineProps<{ value: string; size?: 'tiny' | 'small' | 'medium'; always?: boolean }>(), { size: 'small' })
const emit = defineEmits<{ 'update:value': [string] }>()
const nodes = useNodes()

const options = computed<SelectOption[]>(() =>
  nodes.list.map((n) => ({ label: n.name, value: n.id, state: n.status.state, color: nodes.color(n.id), local: n.kind === 'local' })),
)

function renderLabel(o: SelectOption) {
  return h('span', { class: 'np-opt' }, [
    h('i', { class: ['np-dot', o.local ? 'local' : String(o.state)], style: o.local ? undefined : { borderColor: o.color as string } }),
    String(o.label),
  ])
}
</script>

<template>
  <NSelect
    v-if="always || nodes.list.length > 1"
    class="node-picker"
    :value="props.value"
    :options="options"
    :render-label="renderLabel"
    :size="size"
    :consistent-menu-width="false"
    data-testid="node-picker"
    @update:value="(v: string) => emit('update:value', v)"
  />
</template>

<style>
.node-picker {
  width: 180px;
}
.np-opt {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.np-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #8a8a8a;
  border: 2px solid transparent;
  box-sizing: content-box;
  flex: none;
}
.np-dot.local,
.np-dot.online {
  background: #16a34a;
}
.np-dot.connecting {
  background: #d97706;
}
.np-dot.hostkey-mismatch,
.np-dot.auth-failed {
  background: #dc2626;
}
</style>
