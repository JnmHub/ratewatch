<template>
  <div class="fm-page">
    <header class="fm-head">
      <div><span class="fm-kicker">上游管理</span><h1>可监听上游</h1><p>填入普通 API Key，系统会自动判断平台并确认是否能够持续读取倍率。</p></div>
      <FmButton variant="primary" @click="open">检测并添加</FmButton>
    </header>
    <div class="notice">New API 会使用便宜文本模型进行极小用量检查；Sub2API 可直接读取当前倍率。系统不会主动调用生图模型。</div>
    <div class="filter-bar"><label class="field"><span>搜索上游</span><input v-model.trim="filters.query" placeholder="名称、平台或地址" /></label><label class="field"><span>监听方式</span><FmSelect v-model="filters.state" aria-label="监听方式" :options="filterOptions" /></label></div>
    <FmDataTable :columns="columns" :rows="visibleSources" :loading="loading" empty-text="暂无符合条件的可监听上游">
      <template #cell-monitor_state="{ value }"><FmStatusPill :variant="stateVariant(value)">{{ stateLabel(value) }}</FmStatusPill></template>
      <template #cell-last_rate="{ value }"><span class="mono">{{ value == null ? '待探测' : fmt(value) }}</span></template>
      <template #cell-models="{ value }">{{ value?.length || 0 }} 个模型</template>
      <template #cell-health="{ row }"><HealthStrip :items="row.health_history" /></template>
      <template #actions="{ row }"><FmButton variant="outline" :loading="syncing === row.id" @click="syncNow(row)">立刻同步并查看</FmButton><FmButton variant="danger" :disabled="syncing === row.id" @click="remove(row)">删除</FmButton></template>
    </FmDataTable>

    <FmModal v-model="modal" wide :title="preview ? '确认添加信息' : '添加上游'" kicker="连接检查" :description="preview ? '确认无误后再创建目标并开始监听。' : '系统会自动读取模型，并确认能否持续获得实际倍率。'">
      <form id="source-form" @submit.prevent="preview ? create() : detect()">
        <template v-if="!preview">
          <div class="grid-2">
            <label class="field"><span>上游名称</span><input v-model.trim="form.name" required placeholder="例如：低价文本上游" /></label>
            <label class="field"><span>上游地址</span><input v-model.trim="form.base_url" type="url" required placeholder="https://upstream.example.com" /></label>
          </div>
          <label class="field"><span>普通 Key</span><input v-model="form.key" type="password" required autocomplete="off" /></label>
          <details class="advanced"><summary>高级设置</summary><div class="advanced-body"><label class="field"><span>用于检查的文本模型</span><input v-model.trim="form.probe_model" placeholder="例如 gpt-4o-mini" /><small>Sub2API 可不填；New API 请填写一个便宜且可用的文本模型。</small></label></div></details>
          <label v-if="!form.bind_existing" class="check"><input v-model="form.create_target" type="checkbox" />在我的站点自动创建渠道/账号，并建立同步任务</label>
          <div v-else class="notice">已从站点关系树带入。目标平台不会导出账号的普通 Key，请在上方补填一次；系统校验可监听后才会绑定现有目标分组，且不会重复创建渠道/账号。</div>
          <template v-if="form.create_target || form.bind_existing">
            <div class="target-list">
              <section v-for="(target, index) in form.targets" :key="target.local_id" class="target-card">
                <header><div><span class="fm-kicker">同步目标 {{ index + 1 }}</span><strong>{{ targetName(target) }}</strong></div><FmButton v-if="form.targets.length > 1" type="button" variant="ghost" @click="removeTarget(index)">移除</FmButton></header>
                <div class="grid-2">
                  <label class="field"><span>目标站点</span><FmSelect :model-value="target.site_id" :aria-label="`目标站点 ${index + 1}`" placeholder="请选择目标站点" :options="siteOptions" @update:model-value="value => setTargetSite(target, value)" /></label>
                  <label class="field"><span>目标分组</span><FmSelect v-model="target.group_id" :aria-label="`目标分组 ${index + 1}`" placeholder="请选择目标分组" :options="groupOptions(target.site_id)" /></label>
                </div>
                <div class="grid-2">
                  <label class="field"><span>最低上游倍率</span><input v-model.number="target.minimum_upstream_rate" type="number" min="0" step="0.0001" /><small>上游低于此值时，以此值为计算基准；填 0 表示不限制。</small></label>
                  <label class="field"><span>固定倍率增减值</span><input v-model.number="target.adjustment" type="number" step="0.0001" /><small>该分组独立配置，可为负数；结果 ≤ 0 时拒绝写入。</small></label>
                </div>
              </section>
              <FmButton type="button" variant="outline" @click="addTarget">＋ 添加另一个目标分组</FmButton>
              <div v-if="hasDuplicateTarget" class="notice error">同一个目标分组不能重复添加，请选择不同分组。</div>
            </div>
          </template>
        </template>

        <template v-else>
          <dl class="preview-list">
            <div><dt>识别平台</dt><dd>{{ preview.capability.platform || '未识别' }}</dd></div>
            <div><dt>监听方式</dt><dd><FmStatusPill :variant="stateVariant(preview.capability.monitor_state)">{{ stateLabel(preview.capability.monitor_state) }}</FmStatusPill> {{ preview.capability.message }}</dd></div>
            <div><dt>可用模型</dt><dd><template v-if="preview.capability.models?.length">{{ preview.capability.models.length }} 个 · {{ preview.capability.models.slice(0, 8).join(', ') }}{{ preview.capability.models.length > 8 ? ' …' : '' }}</template><template v-else>暂未取得</template><small v-if="preview.capability.models_message" class="model-source">{{ preview.capability.models_message }}</small></dd></div>
            <div><dt>初始上游倍率</dt><dd class="mono">{{ preview.capability.rate == null ? '首次任务运行时确定' : fmt(preview.capability.rate) }}</dd></div>
          </dl>
          <section v-if="form.create_target || form.bind_existing" class="preview-targets">
            <article v-for="(target, index) in form.targets" :key="target.local_id">
              <header><span>同步目标 {{ index + 1 }}</span><strong>{{ targetName(target) }}</strong></header>
              <dl><div><dt>最低上游倍率</dt><dd class="mono">{{ target.minimum_upstream_rate > 0 ? fmt(target.minimum_upstream_rate) : '不限制' }}</dd></div><div><dt>固定增减值</dt><dd class="mono">{{ target.adjustment >= 0 ? '+' : '' }}{{ fmt(target.adjustment) }}</dd></div><div><dt>预计目标倍率</dt><dd class="mono">{{ preview.capability.rate == null ? '待探测' : fmt(Math.max(preview.capability.rate, target.minimum_upstream_rate || 0) + target.adjustment) }}</dd></div></dl>
            </article>
          </section>
          <div v-if="!canCreate" class="notice error">当前检测结果尚不能持续取得倍率，不会加入监听列表。New API 必须先填写便宜文本模型并成功完成一次实际探测。</div>
          <div v-if="form.create_target || form.bind_existing" class="notice">任一目标分组被删除后，对应任务会停止写入且绝不重建。自动创建只会在确认本预览后执行。</div>
        </template>
        <div v-if="error" class="notice error">{{ error }}</div>
      </form>
      <template #actions>
        <FmButton variant="ghost" @click="preview ? preview = null : close()">{{ preview ? '返回修改' : '取消' }}</FmButton>
        <FmButton variant="primary" type="submit" form="source-form" :loading="busy" :disabled="preview ? !canCreate : !targetReady">{{ preview ? '确认创建' : '开始检测' }}</FmButton>
      </template>
    </FmModal>

    <FmModal v-model="resultModal" wide :title="syncResult ? `${syncResult.source.name} · 本次同步结果` : '本次同步结果'" kicker="立即检查" :description="syncResult?.message || '正在读取上游与关联任务结果。'">
      <template v-if="syncResult">
        <div class="sync-overview">
          <article><span class="label">{{ syncResult.source.last_error ? '最近成功倍率' : '最新上游倍率' }}</span><strong class="mono">{{ syncResult.source.last_rate == null ? '未取得' : fmt(syncResult.source.last_rate) }}</strong><small>最近检查 {{ formatDate(syncResult.source.last_checked_at) }}</small></article>
          <article><span class="label">当前状态</span><div><FmStatusPill :variant="stateVariant(syncResult.source.monitor_state)">{{ stateLabel(syncResult.source.monitor_state) }}</FmStatusPill></div><small>{{ syncResult.source.platform }}</small></article>
          <article><span class="label">关联同步任务</span><strong class="mono">{{ syncResult.enabled_task_count }} / {{ syncResult.linked_task_count }}</strong><small>启用 / 全部</small></article>
        </div>

        <div v-if="syncResult.status === 'skipped'" class="notice">{{ syncResult.message }}</div>
        <div v-else-if="syncResult.source.last_error" class="notice error">本次上游检查：{{ syncResult.source.last_error }}</div>
        <div v-else class="notice ok">本次上游检查正常，已成功读取当前倍率。</div>

        <section class="result-section">
          <div class="result-heading"><div><span class="fm-kicker">AVAILABLE MODELS</span><h3>{{ syncResult.status === 'skipped' ? '演示模型清单' : syncResult.source.last_error ? '最近成功读取的模型' : '当前可用模型' }}</h3></div><span class="model-count">{{ syncResult.source.models?.length || 0 }} 个</span></div>
          <p v-if="syncResult.status === 'skipped'" class="model-note">演示数据仅用于查看界面，没有向外部上游发送请求。</p>
		  <p v-if="syncResult.source.last_error && syncResult.source.models?.length" class="model-note">本次检查失败，以下清单来自最近一次成功读取。</p>
          <div v-if="syncResult.source.models?.length" class="model-list"><span v-for="model in syncResult.source.models" :key="model">{{ model }}</span></div>
          <div v-else class="result-empty">本次没有取得模型清单；倍率检查结果仍会保留。</div>
        </section>

        <section class="result-section">
          <div class="result-heading"><div><span class="fm-kicker">SYNC RESULTS</span><h3>本次任务结果</h3></div><span class="model-count">{{ syncResult.results.length }} 条</span></div>
          <div v-if="syncResult.results.length" class="task-results">
            <article v-for="item in syncResult.results" :key="item.task_id">
              <header><div><strong>{{ item.task_name }}</strong><small>{{ item.site_name || '目标站点' }} / {{ item.group_name || '目标分组' }}</small></div><FmStatusPill :variant="outcomeVariant(item.outcome)">{{ outcomeLabel(item.outcome) }}</FmStatusPill></header>
              <dl><div><dt>任务采用倍率</dt><dd class="mono">{{ item.upstream_rate == null ? '未取得' : fmt(item.upstream_rate) }}</dd></div><div><dt>目标倍率</dt><dd class="mono">{{ item.target_rate == null ? '未写入' : fmt(item.target_rate) }}</dd></div><div><dt>完成时间</dt><dd>{{ formatDate(item.run_at) }}</dd></div></dl>
              <p>{{ item.message }}</p>
            </article>
          </div>
          <div v-else class="result-empty">{{ syncResult.status === 'skipped' ? '这是演示上游，本次没有执行倍率探测或目标写入。' : syncResult.linked_task_count ? '关联任务目前均已暂停，本次只检查上游，没有写入目标站点。' : '当前上游还没有关联同步任务，本次只完成倍率与模型检查。' }}</div>
        </section>
      </template>
      <template #actions><FmButton variant="primary" @click="resultModal = false">知道了</FmButton></template>
    </FmModal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api, toast } from "../api/client.js";
import FmButton from "../ui/freemodel/components/FmButton.vue";
import FmDataTable from "../ui/freemodel/components/FmDataTable.vue";
import FmModal from "../ui/freemodel/components/FmModal.vue";
import FmSelect from "../ui/freemodel/components/FmSelect.vue";
import FmStatusPill from "../ui/freemodel/components/FmStatusPill.vue";
import HealthStrip from "../components/HealthStrip.vue";

const route = useRoute(), router = useRouter(), sources = ref([]), sites = ref([]), inventory = reactive({}), loading = ref(true), modal = ref(false), preview = ref(null), busy = ref(false), error = ref(""), syncing = ref(0), resultModal = ref(false), syncResult = ref(null);
const filters = reactive({ query: "", state: "all" });
const filterOptions = [{ value: "all", label: "全部" }, { value: "direct", label: "Sub2API 直接读取" }, { value: "newapi_probe", label: "New API 定时检查" }];
let targetSequence = 0;
const emptyTarget = values => ({ local_id: ++targetSequence, site_id: 0, group_id: 0, account_id: 0, minimum_upstream_rate: 0, adjustment: 0, ...values });
const emptyForm = () => ({ name: "", base_url: "", key: "", probe_model: "", create_target: true, bind_existing: false, targets: [emptyTarget()] });
const form = reactive(emptyForm());
const columns = [{ key: "name", label: "名称" }, { key: "platform", label: "平台" }, { key: "monitor_state", label: "监听状态" }, { key: "last_rate", label: "当前倍率" }, { key: "health", label: "最近检查" }, { key: "models", label: "模型" }];
const siteOptions = computed(() => sites.value.map(site => ({ value: site.id, label: `${site.name} · ${site.platform}` })));
const canCreate = computed(() => preview.value?.capability?.monitor_state === "direct" || (preview.value?.capability?.monitor_state === "newapi_probe" && preview.value?.capability?.rate != null));
const hasDuplicateTarget = computed(() => form.targets.some((target, index) => target.group_id > 0 && form.targets.findIndex(item => item.group_id === target.group_id) !== index));
const targetReady = computed(() => {
  if (!(form.create_target || form.bind_existing)) return true;
  if (!form.targets.length || form.targets.some(target => target.site_id <= 0 || target.group_id <= 0 || !Number.isFinite(Number(target.minimum_upstream_rate)) || Number(target.minimum_upstream_rate) < 0)) return false;
  return new Set(form.targets.map(target => target.group_id)).size === form.targets.length;
});
const visibleSources = computed(() => sources.value.filter(source => (filters.state === "all" || source.monitor_state === filters.state) && (!filters.query || `${source.name} ${source.platform} ${source.base_url}`.toLowerCase().includes(filters.query.toLowerCase()))));
const groupOptions = siteID => (inventory[siteID] || []).filter(group => group.status !== "deleted").map(group => ({ value: group.id, label: `${group.name} · ${fmt(group.rate)}` }));
const targetName = target => `${sites.value.find(site => site.id === target.site_id)?.name || '未选择站点'} / ${(inventory[target.site_id] || []).find(group => group.id === target.group_id)?.name || '未选择分组'}`;
const setTargetSite = (target, value) => { target.site_id = Number(value || 0); target.group_id = 0; target.account_id = 0; };
const addTarget = () => form.targets.push(emptyTarget());
const removeTarget = index => form.targets.splice(index, 1);
const requestPayload = () => {
  const targets = form.targets.map(({ local_id, ...target }) => target);
  return { ...form, targets };
};

async function load() {
  loading.value = true;
  try {
    [sources.value, sites.value] = await Promise.all([api("/api/sources"), api("/api/sites")]);
    await Promise.all(sites.value.map(async site => inventory[site.id] = await api(`/api/sites/${site.id}/inventory`)));
  } catch (e) { toast(e.message, "error"); }
  finally { loading.value = false; }
}
function open() { Object.assign(form, emptyForm()); preview.value = null; error.value = ""; modal.value = true; }
function close() { modal.value = false; preview.value = null; router.replace({ path: "/sources" }); }
async function detect() {
  busy.value = true; error.value = "";
  try { preview.value = await api("/api/sources/detect", { method: "POST", body: JSON.stringify(requestPayload()) }); toast("上游检测完成，请确认预览信息", "success"); }
  catch (e) { error.value = e.message; toast(e.message, "error"); }
  finally { busy.value = false; }
}
async function create() {
  busy.value = true; error.value = "";
  try {
    await api("/api/sources", { method: "POST", body: JSON.stringify(requestPayload()) });
    const targetCount = (form.create_target || form.bind_existing) ? form.targets.length : 0;
    toast(targetCount ? `上游已添加，并为 ${targetCount} 个目标分组建立同步任务` : "上游已添加");
    close(); Object.assign(form, emptyForm()); await load();
  } catch (e) { error.value = e.message; toast(e.message, "error"); }
  finally { busy.value = false; }
}
async function syncNow(source) {
  syncing.value = source.id;
  try {
    const result = await api(`/api/sources/${source.id}/sync`, { method: "POST" });
    syncResult.value = result;
    resultModal.value = true;
    const index = sources.value.findIndex(item => item.id === source.id);
    if (index >= 0) sources.value[index] = { ...sources.value[index], ...result.source };
    toast(result.message, result.status === "failed" ? "error" : ["partial", "skipped"].includes(result.status) ? "warning" : "success");
  } catch (e) { toast(e.message, "error"); }
  finally { syncing.value = 0; }
}
async function remove(source) {
  if (!confirm(`确认删除上游“${source.name}”？有关联任务时会拒绝删除。`)) return;
  try { await api(`/api/sources/${source.id}`, { method: "DELETE" }); toast("上游已删除"); load(); }
  catch (e) { toast(e.message, "error"); }
}
const fmt = value => Number(value ?? 0).toFixed(4).replace(/0+$/, "").replace(/\.$/, "");
const stateLabel = value => ({ direct: "可直接监听", newapi_probe: "New API 探测", passive_image: "仅生图观测", missing_key: "缺少 Key", unsupported: "不支持", check_failed: "检查失败", demo: "演示数据" }[value] || value);
const stateVariant = value => ({ direct: "ok", newapi_probe: "info", passive_image: "warn", missing_key: "warn", unsupported: "error", check_failed: "error", demo: "neutral" }[value] || "neutral");
const outcomeLabel = value => ({ synced: "已同步", unchanged: "无变化", failed: "失败", blocked: "已阻止", observed: "仅观察", skipped: "已跳过" }[value] || value);
const outcomeVariant = value => ({ synced: "info", unchanged: "ok", failed: "error", blocked: "warn", observed: "info", skipped: "neutral" }[value] || "neutral");
const formatDate = value => value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "未记录";

onMounted(async () => {
  await load();
  if (route.query.base_url) {
    form.name = String(route.query.name || ""); form.base_url = String(route.query.base_url); form.targets = [emptyTarget({ site_id: Number(route.query.site_id || 0), group_id: Number(route.query.group_id || 0), account_id: Number(route.query.account_id || 0) })]; form.create_target = false; form.bind_existing = true; modal.value = true;
  }
});
</script>

<style scoped>
.filter-bar{display:grid;grid-template-columns:minmax(240px,1fr) minmax(170px,.25fr);gap:12px;margin:16px 0}.filter-bar .field{margin:0}
.target-list,.preview-targets{display:grid;gap:12px}.target-card,.preview-targets article{padding:16px;border:1px solid var(--fm-line);border-radius:12px;background:var(--fm-bg-warm)}.target-card>header,.preview-targets article>header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:13px}.target-card>header>div{display:grid;gap:4px}.target-card>header strong,.preview-targets article>header strong{font-size:13px}.preview-targets{margin-top:16px}.preview-targets article>header span{color:var(--fm-ink-muted);font-size:10px}.preview-targets article dl{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:0}.preview-targets article dl div{display:grid;gap:4px;padding:9px;border-radius:8px;background:var(--fm-bg-card)}.preview-targets article dt{color:var(--fm-ink-muted);font-size:9px}.preview-targets article dd{margin:0;font-size:12px}
.sync-overview{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px}.sync-overview article{display:grid;align-content:start;gap:7px;min-height:112px;padding:15px;border:1px solid var(--fm-line-soft);border-radius:10px;background:var(--fm-bg-warm)}.sync-overview strong{font-size:24px;font-variant-numeric:tabular-nums}.sync-overview small{color:var(--fm-ink-muted);font-size:10px}
.result-section{margin-top:22px}.result-heading{display:flex;align-items:end;justify-content:space-between;gap:12px;margin-bottom:11px}.result-heading h3{margin:3px 0 0;font-size:16px}.model-count{padding:4px 7px;border-radius:6px;background:var(--fm-bg-warm);color:var(--fm-ink-muted);font-size:10px}.model-note{margin:-3px 0 9px;color:var(--fm-warn);font-size:11px}
.model-list{display:flex;flex-wrap:wrap;gap:6px;max-height:190px;padding:12px;border:1px solid var(--fm-line-soft);border-radius:10px;overflow:auto}.model-list span{padding:5px 8px;border-radius:6px;background:var(--fm-bg-warm);color:var(--fm-ink-soft);font-size:11px;overflow-wrap:anywhere}
.task-results{display:grid;gap:9px}.task-results article{padding:15px;border:1px solid var(--fm-line);border-radius:11px;background:var(--fm-bg-card)}.task-results header{display:flex;align-items:start;justify-content:space-between;gap:12px}.task-results header>div{display:grid;gap:3px}.task-results header strong{font-size:13px}.task-results header small{color:var(--fm-ink-muted);font-size:10px}.task-results dl{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:13px 0 0}.task-results dl div{display:grid;gap:3px;padding:8px;border-radius:7px;background:var(--fm-bg-warm)}.task-results dt{color:var(--fm-ink-muted);font-size:9px}.task-results dd{margin:0;color:var(--fm-ink-soft);font-size:11px;overflow-wrap:anywhere}.task-results p{margin:10px 0 0;color:var(--fm-ink-soft);font-size:11px}
.result-empty{padding:20px;border:1px dashed var(--fm-line);border-radius:10px;color:var(--fm-ink-muted);font-size:12px;text-align:center}
.model-source{display:block;margin-top:5px;color:var(--fm-ink-muted);font-size:10px;line-height:1.5}
@media(max-width:640px){.filter-bar,.sync-overview,.task-results dl,.preview-targets article dl{grid-template-columns:1fr}.sync-overview article{min-height:0}}
</style>
