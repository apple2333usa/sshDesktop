<script setup lang="ts">
// 时间序列折线图（ECharts）：单一纵轴、2px 线、十字准线提示；配色区分浅色 / 深色。
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { use, init, type ECharts } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { usePrefs } from '@/stores/prefs'
import { formatTime } from '@/i18n'

use([LineChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer])

export interface ChartSeries {
  name: string
  data: [number, number][]
}

const props = withDefaults(
  defineProps<{
    series: ChartSeries[]
    format: (v: number) => string
    max?: number
    area?: boolean
    /** 跨度超过一天时横轴显示日期 */
    long?: boolean
  }>(),
  { max: undefined, area: false, long: false },
)

const PALETTE = {
  light: ['#2a78d6', '#eb6834', '#1baf7a'],
  dark: ['#3987e5', '#d95926', '#199e70'],
}

const prefs = usePrefs()
const el = ref<HTMLElement | null>(null)
let chart: ECharts | null = null
let ro: ResizeObserver | null = null

const pad = (x: number) => String(x).padStart(2, '0')
function axisLabel(v: number) {
  const d = new Date(v)
  return props.long ? `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}` : `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function option() {
  const dark = prefs.dark
  const text = dark ? '#c5c5c5' : '#5d5d5d'
  const grid = dark ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.07)'
  const colors = dark ? PALETTE.dark : PALETTE.light
  return {
    animation: false,
    color: colors,
    textStyle: { fontFamily: 'inherit' },
    grid: { left: 8, right: 16, top: props.series.length > 1 ? 30 : 12, bottom: 4, containLabel: true },
    legend: props.series.length > 1 ? { top: 0, right: 8, icon: 'roundRect', itemWidth: 12, itemHeight: 4, textStyle: { color: text } } : undefined,
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'line', lineStyle: { color: text, type: 'dashed' } },
      backgroundColor: dark ? '#2b2b2b' : '#ffffff',
      borderColor: grid,
      textStyle: { color: dark ? '#fff' : '#1b1b1b', fontSize: 12 },
      formatter: (items: { axisValue: number; marker: string; seriesName: string; value: [number, number] }[]) => {
        if (!items.length) return ''
        const head = formatTime(items[0].axisValue, true)
        return [head, ...items.map((i) => `${i.marker}${i.seriesName}　<b>${props.format(i.value[1])}</b>`)].join('<br/>')
      },
    },
    xAxis: {
      type: 'time',
      axisLine: { lineStyle: { color: grid } },
      axisTick: { show: false },
      axisLabel: { color: text, fontSize: 11, hideOverlap: true, formatter: axisLabel },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      max: props.max,
      splitNumber: 3,
      axisLabel: { color: text, fontSize: 11, formatter: (v: number) => props.format(v) },
      splitLine: { lineStyle: { color: grid } },
    },
    series: props.series.map((s) => ({
      name: s.name,
      type: 'line',
      data: s.data,
      showSymbol: false,
      symbolSize: 8,
      lineStyle: { width: 2 },
      emphasis: { disabled: true },
      areaStyle: props.area ? { opacity: 0.12 } : undefined,
      connectNulls: false,
    })),
  }
}

function render() {
  chart?.setOption(option() as never, { notMerge: true, lazyUpdate: true })
}

onMounted(() => {
  chart = init(el.value!, undefined, { renderer: 'canvas' })
  render()
  ro = new ResizeObserver(() => chart?.resize())
  ro.observe(el.value!)
})
onUnmounted(() => {
  ro?.disconnect()
  chart?.dispose()
  chart = null
})

watch(() => [props.series, props.max, props.long, prefs.dark], render, { deep: false })
defineExpose({ render })
</script>

<template>
  <div ref="el" class="chart" />
</template>

<style scoped>
.chart {
  width: 100%;
  height: 100%;
  min-height: 120px;
}
</style>
