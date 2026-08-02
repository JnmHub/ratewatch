<template>
  <div class="health-wrap">
    <div class="health-strip" :aria-label="summary">
      <button
        v-for="item in ordered"
        :key="item.id"
        type="button"
        class="health-cell"
        :class="item.state"
        :aria-label="accessibleTip(item)"
        @mouseenter="showTip(item, $event.currentTarget)"
        @mouseleave="hideTip"
        @focus="showTip(item, $event.currentTarget)"
        @blur="hideTip"
        @click="showTip(item, $event.currentTarget)"
      />
      <span v-for="n in emptyCells" :key="`empty-${n}`" class="health-cell empty" aria-hidden="true" />
    </div>
    <small>{{ summary }}</small>
  </div>

  <Teleport to="body">
    <aside
      v-if="tooltip"
      class="health-tooltip"
      :class="[`state-${tooltip.item.state}`, tooltip.below ? 'below' : 'above']"
      :style="{ left: `${tooltip.left}px`, top: `${tooltip.top}px` }"
      role="tooltip"
    >
      <header>
        <i />
        <strong>{{ stateTitle(tooltip.item) }}</strong>
      </header>
      <dl>
        <div><dt>检查时间</dt><dd>{{ formatTime(tooltip.item.created_at) }}</dd></div>
        <div><dt>当次倍率</dt><dd class="mono">{{ formatRate(tooltip.item.rate) }}</dd></div>
        <div class="detail"><dt>{{ detailLabel(tooltip.item) }}</dt><dd>{{ detail(tooltip.item) }}</dd></div>
      </dl>
    </aside>
  </Teleport>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

const props = defineProps({ items: { type: Array, default: () => [] } });
const tooltip = ref(null);
const ordered = computed(() => [...props.items].reverse().slice(-20));
const emptyCells = computed(() => ordered.value.length ? 0 : 20);
const latest = computed(() => props.items?.[0]);
const labels = {
  healthy: "连接正常，倍率无变化",
  synced: "倍率变化并已同步",
  changed: "发现倍率变化，尚未写入",
  write_failed: "上游检查正常，目标写入失败",
  failed: "上游检查失败",
  skipped: "本次检查已跳过",
};
const titles = {
  healthy: "连接正常",
  synced: "同步成功",
  changed: "发现变化",
  write_failed: "目标写入失败",
  failed: "上游检查失败",
  skipped: "本次已跳过",
};
const defaultDetails = {
  healthy: "已成功访问上游，本次倍率与上次记录一致。",
  synced: "检测到倍率变化，目标分组已经按任务规则更新。",
  changed: "检测到倍率变化，但当前尚未完成目标写入。",
  write_failed: "已取得上游倍率，但未能写入目标站点，请查看异常日志。",
  failed: "本次没有取得有效倍率，请查看上游返回的失败原因。",
  skipped: "本轮未执行倍率探测或目标写入。",
};
const summary = computed(() => latest.value ? labels[latest.value.state] || latest.value.message || "检查已完成" : "等待首次检查");

function stateTitle(item) {
  return titles[item.state] || item.state || "检查结果";
}
function detail(item) {
  return item.message || defaultDetails[item.state] || "本次检查已完成。";
}
function detailLabel(item) {
  if (["failed", "write_failed"].includes(item.state)) return "具体错误";
  if (item.state === "synced") return "推送详情";
  return "结果说明";
}
function formatTime(value) {
  if (!value) return "时间未记录";
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}
function formatRate(value) {
  if (value == null) return "未取得";
  return Number(value).toFixed(4).replace(/0+$/, "").replace(/\.$/, "");
}
function accessibleTip(item) {
  return `${stateTitle(item)}；检查时间 ${formatTime(item.created_at)}；当次倍率 ${formatRate(item.rate)}；${detail(item)}`;
}
function showTip(item, target) {
  const rect = target.getBoundingClientRect();
  const width = Math.min(286, window.innerWidth - 28);
  const left = Math.max(14, Math.min(window.innerWidth - width - 14, rect.left + rect.width / 2 - width / 2));
  const below = rect.top < 190;
  tooltip.value = { item, below, left, top: below ? rect.bottom + 10 : rect.top - 10 };
}
function hideTip() {
  tooltip.value = null;
}
onMounted(() => {
  window.addEventListener("resize", hideTip);
  window.addEventListener("scroll", hideTip, true);
});
onBeforeUnmount(() => {
  window.removeEventListener("resize", hideTip);
  window.removeEventListener("scroll", hideTip, true);
});
</script>

<style scoped>
.health-wrap{display:grid;gap:5px;margin-top:5px}.health-wrap small{color:var(--fm-ink-muted);font-size:10px}.health-strip{display:grid;grid-template-columns:repeat(20,18px);gap:4px;max-width:100%;height:21px;overflow:visible}.health-cell{position:relative;width:18px;height:21px;padding:0;border:1px solid color-mix(in srgb,currentColor 14%,transparent);border-radius:3px;background:var(--fm-line);color:var(--fm-ink-muted);cursor:help;transition:transform .11s cubic-bezier(.2,.9,.25,1.35),box-shadow .11s ease,filter .11s ease}.health-cell:not(.empty)::after{position:absolute;inset:-4px;border:1px solid currentColor;border-radius:6px;content:"";opacity:0;pointer-events:none;transform:scale(.72);transition:opacity .1s ease,transform .1s ease}.health-cell:hover,.health-cell:focus-visible{z-index:2;filter:saturate(1.16) brightness(1.08);outline:none;transform:translateY(-3px) scale(1.18);box-shadow:0 0 0 3px color-mix(in srgb,currentColor 18%,transparent),0 7px 15px color-mix(in srgb,currentColor 34%,transparent)}.health-cell:hover::after,.health-cell:focus-visible::after{opacity:.56;transform:scale(1)}.health-cell.healthy{background:#22c55e;color:#15803d}.health-cell.synced{background:#3b82f6;color:#2563eb}.health-cell.changed{background:#f59e0b;color:#d97706}.health-cell.write_failed{background:#f97316;color:#ea580c}.health-cell.failed{background:#ef4444;color:#dc2626}.health-cell.skipped,.health-cell.empty{background:var(--fm-line);color:var(--fm-ink-muted)}.health-cell.empty{cursor:default}.health-tooltip{position:fixed;z-index:1300;width:min(286px,calc(100vw - 28px));padding:14px 15px 13px;border:1px solid var(--fm-line);border-radius:11px;background:var(--fm-bg-card);color:var(--fm-ink);box-shadow:var(--fm-modal-shadow);pointer-events:none}.health-tooltip.above{transform:translateY(-100%)}.health-tooltip::after{position:absolute;left:50%;width:9px;height:9px;border-right:1px solid var(--fm-line);border-bottom:1px solid var(--fm-line);background:var(--fm-bg-card);content:""}.health-tooltip.above::after{bottom:-5px;transform:translateX(-50%) rotate(45deg)}.health-tooltip.below::after{top:-5px;transform:translateX(-50%) rotate(225deg)}.health-tooltip header{display:flex;align-items:center;gap:8px;padding-bottom:10px;border-bottom:1px solid var(--fm-line-soft)}.health-tooltip header i{width:8px;height:8px;border-radius:3px;background:var(--fm-ink-muted)}.health-tooltip header strong{font-size:13px;font-weight:600}.health-tooltip.state-healthy header i{background:#22c55e}.health-tooltip.state-synced header i{background:#3b82f6}.health-tooltip.state-changed header i{background:#f59e0b}.health-tooltip.state-write_failed header i{background:#f97316}.health-tooltip.state-failed header i{background:#ef4444}.health-tooltip dl{display:grid;gap:7px;margin:10px 0 0}.health-tooltip dl div{display:grid;grid-template-columns:58px minmax(0,1fr);gap:10px}.health-tooltip dt{color:var(--fm-ink-muted);font-size:10px}.health-tooltip dd{margin:0;color:var(--fm-ink-soft);font-size:11px;text-align:right;overflow-wrap:anywhere}.health-tooltip .detail dd{text-align:left;line-height:1.5}.mono{font-variant-numeric:tabular-nums}@media(max-width:640px){.health-strip{grid-template-columns:repeat(20,14px)}.health-cell{width:14px}.health-tooltip{padding:13px}}
</style>
