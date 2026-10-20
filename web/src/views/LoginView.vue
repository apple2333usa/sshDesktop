<script setup lang="ts">
// 登录页：Windows 11 锁屏风格。点击或按键后显示登录框。
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NInput } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { ApiError, Codes } from '@/api/http'
import { useSession } from '@/stores/session'
import { usePrefs } from '@/stores/prefs'
import { wallpaperCSS } from '@/theme/wallpapers'
import { preferredShell } from '@/router'

const { t } = useI18n()
const session = useSession()
const prefs = usePrefs()
const route = useRoute()
const router = useRouter()

const unlocked = ref(false)
const username = ref('')
const password = ref('')
const error = ref(route.query.expired ? t('login.expired') : '')
const loading = ref(false)
const now = ref(new Date())
const userInput = ref<InstanceType<typeof NInput> | null>(null)
let timer: number | undefined

const time = computed(() => now.value.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }))
const date = computed(() => now.value.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' }))
const bg = computed(() => wallpaperCSS(prefs.p.wallpaper, prefs.dark))

async function unlock() {
  if (unlocked.value) return
  unlocked.value = true
  await nextTick()
  userInput.value?.focus()
}

function onKey(e: KeyboardEvent) {
  if (!unlocked.value && !e.ctrlKey && !e.altKey && !e.metaKey) unlock()
}

onMounted(() => {
  timer = window.setInterval(() => (now.value = new Date()), 1000)
  window.addEventListener('keydown', onKey)
  if (error.value) unlock()
})
onUnmounted(() => {
  clearInterval(timer)
  window.removeEventListener('keydown', onKey)
})

async function submit() {
  if (!username.value || !password.value || loading.value) return
  loading.value = true
  error.value = ''
  try {
    await session.login(username.value, password.value)
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    router.replace(redirect && redirect.startsWith('/') ? redirect : { name: preferredShell() })
  } catch (e) {
    const err = e as ApiError
    error.value = err.message
    if (err.code === Codes.BadCredentials) password.value = ''
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="lock" :style="{ backgroundImage: bg }" @click="unlock">
    <div class="veil" :class="{ on: unlocked }" />
    <Transition name="clock">
      <div v-if="!unlocked" class="clock">
        <div class="time">{{ time }}</div>
        <div class="date">{{ date }}</div>
        <div class="hint">{{ t('login.hint') }}</div>
      </div>
    </Transition>
    <Transition name="form">
      <form v-if="unlocked" class="form" @submit.prevent="submit" @click.stop>
        <div class="avatar">
          <svg viewBox="0 0 48 48"><circle cx="24" cy="18" r="9" fill="currentColor" /><path d="M7 44c0-9 8-15 17-15s17 6 17 15z" fill="currentColor" /></svg>
        </div>
        <div class="title">sshDesktop</div>
        <NInput
          ref="userInput"
          v-model:value="username"
          :placeholder="t('login.username')"
          :input-props="{ autocomplete: 'username', name: 'username' }"
          size="large"
          data-testid="login-username"
        />
        <NInput
          v-model:value="password"
          type="password"
          show-password-on="click"
          :placeholder="t('login.password')"
          :input-props="{ autocomplete: 'current-password', name: 'password' }"
          size="large"
          data-testid="login-password"
          @keyup.enter="submit"
        />
        <div v-if="error" class="error" data-testid="login-error">{{ error }}</div>
        <NButton type="primary" size="large" attr-type="submit" :loading="loading" block data-testid="login-submit">
          {{ t('login.submit') }}
        </NButton>
        <div class="tip">{{ t('login.tip') }}</div>
      </form>
    </Transition>
  </div>
</template>

<style scoped>
.lock {
  position: fixed;
  inset: 0;
  background-size: cover;
  background-position: center;
  color: #fff;
  user-select: none;
  overflow: hidden;
}
.veil {
  position: absolute;
  inset: 0;
  transition: backdrop-filter 0.4s, background 0.4s;
}
.veil.on {
  backdrop-filter: blur(24px);
  background: rgba(0, 0, 0, 0.25);
}
.clock {
  position: absolute;
  left: 0;
  right: 0;
  top: 12vh;
  text-align: center;
  text-shadow: 0 2px 12px rgba(0, 0, 0, 0.35);
}
.time {
  font-size: clamp(64px, 12vw, 120px);
  font-weight: 600;
  line-height: 1;
}
.date {
  font-size: clamp(18px, 2.4vw, 26px);
  margin-top: 10px;
}
.hint {
  margin-top: 40vh;
  font-size: 14px;
  opacity: 0.85;
}
.form {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  width: min(340px, calc(100vw - 32px));
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: stretch;
}
.avatar {
  width: 120px;
  height: 120px;
  margin: 0 auto 4px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.25);
  color: rgba(255, 255, 255, 0.9);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  overflow: hidden;
}
.avatar svg {
  width: 90px;
  height: 90px;
}
.title {
  text-align: center;
  font-size: 24px;
  font-weight: 600;
  margin-bottom: 8px;
  text-shadow: 0 1px 6px rgba(0, 0, 0, 0.3);
}
.error {
  background: rgba(196, 43, 28, 0.85);
  padding: 8px 12px;
  border-radius: 4px;
  font-size: 13px;
}
.tip {
  text-align: center;
  font-size: 12px;
  opacity: 0.8;
  text-shadow: 0 1px 4px rgba(0, 0, 0, 0.4);
}
.clock-leave-active,
.form-enter-active {
  transition: all 0.3s ease;
}
.clock-leave-to {
  opacity: 0;
  transform: translateY(-40px);
}
.form-enter-from {
  opacity: 0;
  transform: translate(-50%, -40%);
}
</style>
