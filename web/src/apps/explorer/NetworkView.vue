<script setup lang="ts">
// “网络”：每台远程服务器一个网络位置，图标上显示在线状态。
import { useI18n } from 'vue-i18n'
import { NEmpty, NTag } from 'naive-ui'
import { useNodes } from '@/stores/nodes'
import SdIcon from '@/components/SdIcon.vue'

const emit = defineEmits<{ open: [string] }>()
const { t } = useI18n()
const nodes = useNodes()
</script>

<template>
  <div class="wrap sd-scroll" data-testid="network-view">
    <NEmpty v-if="!nodes.remote.length" class="empty" :description="t('explorer.networkEmpty')" />
    <div class="grid">
      <div
        v-for="n in nodes.remote"
        :key="n.id"
        class="loc"
        :class="{ off: n.status.state !== 'online' }"
        :data-testid="'network-' + n.id"
        @dblclick="emit('open', n.id)"
      >
        <div class="ic">
          <SdIcon name="server" :size="44" />
          <i class="state" :class="n.status.state" />
        </div>
        <div class="info">
          <div class="name sd-ellipsis" :style="{ color: n.color || undefined }">{{ n.name }}</div>
          <div class="sd-muted small sd-ellipsis">{{ n.username }}@{{ n.host }}:{{ n.port }}</div>
          <NTag v-if="n.status.state !== 'online'" size="tiny" :bordered="false" :type="n.status.state === 'connecting' ? 'warning' : 'error'">
            {{ t('servers.state.' + n.status.state) }}
          </NTag>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.wrap {
  padding: 12px 16px;
}
.empty {
  margin-top: 40px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 8px;
}
.loc {
  display: flex;
  gap: 12px;
  align-items: center;
  padding: 10px;
  border-radius: 4px;
  border: 1px solid transparent;
}
.loc:hover {
  background: var(--sd-hover);
  border-color: var(--sd-border);
}
.loc.off .ic {
  opacity: 0.6;
}
.ic {
  position: relative;
}
.state {
  position: absolute;
  right: -2px;
  bottom: 0;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid var(--sd-surface);
  background: #8a8a8a;
}
.state.online {
  background: #16a34a;
}
.state.connecting {
  background: #d97706;
}
.state.hostkey-mismatch,
.state.auth-failed {
  background: #dc2626;
}
.info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  align-items: flex-start;
}
.name {
  font-weight: 600;
  max-width: 100%;
}
.small {
  font-size: 12px;
  max-width: 100%;
}
</style>
