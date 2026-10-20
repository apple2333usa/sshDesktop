<script setup lang="ts">
// 分类柱状图（ECharts），用于按天的流量统计；多个系列堆叠。配色与 LineChart 一致。
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { use, init, type ECharts } from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { usePrefs } from '@/stores/prefs'

use([BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer])

const props = defineProps<{
  labels: string[]
  series: { name: string; data: number[] }[]
  format: (v: number) => string
}>()

const PALETTE = {
  light: ['#2a78d6', '#eb6834', '#1baf7a'],
  dark: ['#3987e5', '#d95926', '#199e70'],
}

const prefs = usePrefs()
const el = ref<HTMLElement | null>(null)
let chart: ECharts | null = null
let ro: ResizeObserver | null = null

function option() {
  const dark = prefs.dark
  const text = dark ? '#c5c5c5' : '#5d5d5d'
  const grid = dark ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.07)'
  return {
    animation: false,
    color: dark ? PALETTE.dark : PALETTE.light,
    textStyle: { fontFamily: 'inherit' },
    grid: { left: 8, right: 16, top: 30, bottom: 4, containLabel: true },
    legend: { top: 0, right: 8, icon: 'roundRect', itemWidth: 12, itemHeight: 8, textStyle: { color: text } },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      backgroundColor: dark ? '#2b2b2b' : '#ffffff',
      borderColor: grid,
      textStyle: { color: dark ? '#fff' : '#1b1b1b', fontSize: 12 },
      formatter: (items: { axisValue: string; marker: string; seriesName: string; value: number }[]) => {
        if (!items.length) return ''
        const sum = items.reduce((a, i) => a + i.value, 0)
        return [items[0].axisValue, ...items.map((i) => `${i.marker}${i.seriesName}　<b>${props.format(i.value)}</b>`), `合计　<b>${props.format(sum)}</b>`].join('<br/>')
      },
    },
    xAxis: {
      type: 'category',
      data: props.labels,
      axisLine: { lineStyle: { color: grid } },
      axisTick: { show: false },
      axisLabel: { color: text, fontSize: 11, hideOverlap: true },
    },
    yAxis: {
      type: 'value',
      min: 0,
      splitNumber: 3,
      axisLabel: { color: text, fontSize: 11, formatter: (v: number) => props.format(v) },
      splitLine: { lineStyle: { color: grid } },
    },
    series: props.series.map((s) => ({ name: s.name, type: 'bar', stack: 'all', data: s.data, barMaxWidth: 18, emphasis: { disabled: true } })),
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

watch(() => [props.series, props.labels, prefs.dark], render)
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
