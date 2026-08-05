<template>
  <div class="rate-chart" :aria-label="summary">
    <Line v-if="points.length" :data="chartData" :options="chartOptions" />
    <div v-else class="empty-chart">
      <span>等待倍率记录</span>
      <small>完成首次有效检查后显示变化趋势</small>
    </div>
  </div>
</template>

<script setup>
import { computed } from "vue";
import {
  CategoryScale,
  Chart as ChartJS,
  Filler,
  LineElement,
  LinearScale,
  PointElement,
  Tooltip,
} from "chart.js";
import { Line } from "vue-chartjs";

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Filler, Tooltip);

const props = defineProps({ items: { type: Array, default: () => [] } });
const points = computed(() => [...props.items]
  .reverse()
  .filter(item => item.rate != null && Number.isFinite(Number(item.rate)))
  .slice(-30));
const formatRate = value => Number(value).toFixed(4).replace(/0+$/, "").replace(/\.$/, "");
const formatTime = value => new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
const stateLabel = value => ({ healthy: "检查正常", synced: "已同步", changed: "发现变化", write_failed: "写入失败", failed: "检查失败", skipped: "已跳过" }[value] || "已检查");
const summary = computed(() => points.value.length
  ? `最近 ${points.value.length} 次有效倍率记录，当前倍率 ${formatRate(points.value.at(-1).rate)}`
  : "暂无有效倍率历史");
const chartData = computed(() => ({
  labels: points.value.map(item => formatTime(item.created_at)),
  datasets: [{
    label: "上游倍率",
    data: points.value.map(item => Number(item.rate)),
    borderColor: "#19c37d",
    backgroundColor: "rgba(25, 195, 125, 0.1)",
    pointBackgroundColor: points.value.map(item => item.state === "synced" ? "#3b82f6" : "#19c37d"),
    pointBorderColor: points.value.map(item => item.state === "synced" ? "#3b82f6" : "#19c37d"),
    pointRadius: points.value.length === 1 ? 4 : 2.5,
    pointHoverRadius: 5,
    borderWidth: 2,
    tension: 0.25,
    fill: true,
    spanGaps: false,
  }],
}));
const chartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: { duration: 220 },
  interaction: { intersect: false, mode: "index" },
  plugins: {
    legend: { display: false },
    tooltip: {
      displayColors: false,
      callbacks: {
        title: items => items[0]?.label || "",
        label: context => `上游倍率 ${formatRate(context.parsed.y)}`,
        afterLabel: context => {
          const item = points.value[context.dataIndex];
          return `${stateLabel(item?.state)}${item?.message ? ` · ${item.message}` : ""}`;
        },
      },
    },
  },
  scales: {
    x: { display: false, grid: { display: false } },
    y: {
      beginAtZero: false,
      border: { display: false },
      grid: { color: "rgba(128, 128, 128, 0.12)", drawTicks: false },
      ticks: { color: "#7b858c", padding: 8, maxTicksLimit: 4, callback: value => formatRate(value) },
    },
  },
}));
</script>

<style scoped>
.rate-chart{height:160px;min-width:0}.rate-chart canvas{width:100%!important;height:100%!important}.empty-chart{display:grid;place-content:center;height:100%;border:1px dashed var(--fm-line);border-radius:10px;background:var(--fm-bg-warm);color:var(--fm-ink-muted);text-align:center}.empty-chart span{font-size:12px;font-weight:600}.empty-chart small{margin-top:4px;font-size:10px}@media(max-width:480px){.rate-chart{height:145px}}
</style>
