import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { i18n } from './i18n'
import './theme/global.css'

// 切换访问方式后的一次性确认令牌：先从地址栏去掉，交给 AccessConfirm 提交
const url = new URL(window.location.href)
const accessToken = url.searchParams.get('confirm-access') ?? ''
if (accessToken) {
  url.searchParams.delete('confirm-access')
  window.history.replaceState(null, '', url.pathname + url.search + url.hash)
}

createApp(App, { accessToken }).use(createPinia()).use(router).use(i18n).mount('#app')
