<template>
  <div class="fm-page">
    <header class="fm-head">
      <div>
        <span class="fm-kicker">我的站点</span>
        <h1>站点与关系树</h1>
        <p>添加你自己的站点后，会自动读取分组、渠道或账号及当前倍率，并整理成清晰的关联关系。</p>
      </div>
      <FmButton variant="primary" @click="modal = true">添加站点</FmButton>
    </header>

    <div class="filter-bar">
      <label class="field"><span>筛选关系树</span><input v-model.trim="filters.query" placeholder="分组、账号、平台或上游地址" /></label>
      <label class="field"><span>监听状态</span><FmSelect v-model="filters.state" aria-label="监听状态" :options="filterOptions" /></label>
    </div>

    <FmStatePanel v-if="loading" state="loading" />
    <FmStatePanel v-else-if="!sites.length" state="empty" title="还没有自有站点" description="先添加你的 New API 或 Sub2API，系统会立即校验管理员连接并导入关系树">
      <template #action><FmButton variant="primary" @click="modal = true">添加第一个站点</FmButton></template>
    </FmStatePanel>

    <section v-for="site in sites" :key="site.id" class="section">
      <div class="section-head">
        <div>
          <h2>{{ site.name }} <FmStatusPill :variant="site.status === 'ready' ? 'ok' : 'error'">{{ site.status === 'ready' ? '已连接' : '连接异常' }}</FmStatusPill></h2>
          <p class="mono">{{ site.platform }} · {{ site.base_url }}</p>
        </div>
        <div class="fm-actions">
          <FmButton variant="outline" :loading="importing === site.id" @click="refresh(site)">重新导入</FmButton>
          <FmButton variant="danger" @click="remove(site)">删除</FmButton>
        </div>
      </div>
      <div v-if="site.last_error" class="notice error">{{ site.last_error }}</div>
      <div class="tree">
        <details v-for="g in visibleGroups(site)" :key="g.id" class="tree-group" open>
          <summary>
            <span><strong>{{ g.name }}</strong> <small class="muted mono">ID {{ g.external_id }}</small></span>
            <span class="mono">现有倍率 {{ fmt(g.rate) }}</span>
          </summary>
          <div class="tree-items">
            <div v-if="g.status === 'deleted'" class="notice error">目标站点已删除该分组，系统不会重新创建，也不会继续写入。</div>
            <div v-if="!g.accounts?.length" class="notice">该分组没有导入到渠道/账号。目标分组仍可作为同步写入目标。</div>
            <div v-if="conflict(g)" class="notice">同一分组内账号观测倍率不一致。启用任务后只使用受支持账号中的最高有效倍率，未监听账号不会参与计算。</div>
            <article v-for="a in g.accounts" :key="a.id" class="tree-item">
              <div>
                <strong>{{ a.name }}</strong>
                <div class="muted mono">{{ a.platform || '待识别' }} · {{ a.base_url || '未返回上游地址' }}</div>
                <div class="muted">{{ a.models?.length || 0 }} 个模型<span v-if="a.rate != null"> · 已观测倍率 {{ fmt(a.rate) }}</span></div>
                <div v-if="!a.secret_mask" class="muted">目标平台未导出普通 Key，添加监听时需要用户补填一次。</div>
              </div>
              <div class="account-action">
                <FmStatusPill :variant="stateVariant(a.monitor_state)">{{ stateLabel(a.monitor_state) }}</FmStatusPill>
                <RouterLink v-if="a.base_url" class="listen-link" :to="{ path: '/sources', query: { name: a.name, base_url: a.base_url, site_id: site.id, group_id: g.id, account_id: a.id } }">配置监听</RouterLink>
              </div>
            </article>
          </div>
        </details>
      </div>
    </section>

    <FmModal v-model="modal" title="添加我的站点" kicker="连接站点" description="保存后会立即检查连接并读取现有分组和账号。">
      <form id="site-form" @submit.prevent="create">
        <label class="field"><span>站点名称</span><input v-model.trim="form.name" required placeholder="例如：主站" /></label>
        <label class="field"><span>站点类型</span><FmSelect v-model="form.platform" aria-label="站点类型" :options="platformOptions" /></label>
        <label class="field"><span>站点域名</span><input v-model.trim="form.base_url" type="url" required placeholder="https://api.example.com" /></label>
        <label class="field"><span>永久管理员 Key</span><input v-model="form.admin_key" type="password" required autocomplete="off" /></label>
        <details class="advanced"><summary>高级设置</summary><div class="advanced-body grid-2 aligned-fields">
          <label class="field"><span>管理员用户 ID</span><input v-model="form.admin_user_id" :disabled="form.platform === 'sub2api'" /><small>{{ form.platform === 'sub2api' ? 'Sub2API 不需要此项' : 'New API 通常保持 1' }}</small></label>
          <label class="field"><span>鉴权方式（自动）</span><input v-model="form.admin_header" readonly /><small>已根据站点类型自动匹配，无需手工修改</small></label>
        </div></details>
        <div v-if="error" class="notice error">{{ error }}</div>
      </form>
      <template #actions>
        <FmButton variant="ghost" @click="modal = false">取消</FmButton>
        <FmButton variant="primary" type="submit" form="site-form" :loading="saving">保存并导入</FmButton>
      </template>
    </FmModal>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref, watch } from "vue";
import { api, toast } from "../api/client.js";
import FmButton from "../ui/freemodel/components/FmButton.vue";
import FmSelect from "../ui/freemodel/components/FmSelect.vue";
import FmModal from "../ui/freemodel/components/FmModal.vue";
import FmStatePanel from "../ui/freemodel/components/FmStatePanel.vue";
import FmStatusPill from "../ui/freemodel/components/FmStatusPill.vue";

const loading = ref(true), sites = ref([]), inventories = reactive({}), modal = ref(false), saving = ref(false), importing = ref(0), error = ref("");
const filters = reactive({ query: "", state: "all" });
const filterOptions = [{ value: "all", label: "全部状态" }, { value: "monitorable", label: "可监听" }, { value: "attention", label: "需要处理" }];
const platformOptions = [{ value: "newapi", label: "New API" }, { value: "sub2api", label: "Sub2API" }];
const form = reactive({ name: "", platform: "newapi", base_url: "", admin_key: "", admin_user_id: "1", admin_header: "Authorization" });
watch(() => form.platform, platform => {
  form.admin_header = platform === "sub2api" ? "x-api-key" : "Authorization";
  form.admin_user_id = platform === "sub2api" ? "" : "1";
});

async function load() {
  loading.value = true;
  try {
    sites.value = await api("/api/sites");
    await Promise.all(sites.value.map(async site => inventories[site.id] = await api(`/api/sites/${site.id}/inventory`)));
  } catch (e) { toast(e.message, "error"); }
  finally { loading.value = false; }
}
async function create() {
  saving.value = true; error.value = "";
  try {
    const site = await api("/api/sites", { method: "POST", body: JSON.stringify(form) });
    modal.value = false;
    Object.assign(form, { name: "", platform: "newapi", base_url: "", admin_key: "", admin_user_id: "1", admin_header: "Authorization" });
    toast(site.status === "ready" ? "站点已连接并完成首次导入" : "站点已保存，但首次导入失败，请查看连接异常", site.status === "ready" ? "ok" : "warning");
    await load();
  } catch (e) { error.value = e.message; toast(e.message, "error"); }
  finally { saving.value = false; }
}
async function refresh(site) {
  importing.value = site.id;
  try { inventories[site.id] = await api(`/api/sites/${site.id}/import`, { method: "POST" }); toast("关系树已更新"); await load(); }
  catch (e) { toast(e.message, "error"); }
  finally { importing.value = 0; }
}
async function remove(site) {
  if (!confirm(`确认删除站点“${site.name}”及其本地关系和任务？`)) return;
  try { await api(`/api/sites/${site.id}`, { method: "DELETE" }); toast("站点已删除"); load(); }
  catch (e) { toast(e.message, "error"); }
}
function conflict(group) { const values = group.accounts?.map(a => a.rate).filter(v => v != null) || []; return new Set(values.map(Number)).size > 1; }
function visibleGroups(site) {
  const query = filters.query.toLowerCase();
  const monitorable = new Set(["direct", "newapi_probe", "passive_image"]);
  return (inventories[site.id] || []).flatMap(group => {
    const groupMatches = !query || `${group.name} ${group.external_id}`.toLowerCase().includes(query);
    const accounts = (group.accounts || []).filter(account => {
      const stateMatches = filters.state === "all" || (filters.state === "monitorable" ? monitorable.has(account.monitor_state) : !monitorable.has(account.monitor_state));
      const textMatches = groupMatches || `${account.name} ${account.platform} ${account.base_url}`.toLowerCase().includes(query);
      return stateMatches && textMatches;
    });
    return groupMatches && filters.state === "all" || accounts.length ? [{ ...group, accounts }] : [];
  });
}
const fmt = value => Number(value ?? 0).toFixed(4).replace(/0+$/, "");
const stateLabel = value => ({ direct: "可直接监听", newapi_probe: "探测监听", passive_image: "仅生图观测", missing_key: "缺少普通 Key", unsupported: "不支持", check_failed: "检查失败" }[value] || "尚未检查");
const stateVariant = value => ({ direct: "ok", newapi_probe: "info", passive_image: "warn", missing_key: "warn", unsupported: "error", check_failed: "error" }[value] || "neutral");
onMounted(load);
</script>

<style scoped>
.account-action{display:flex;align-items:center;justify-content:flex-end;gap:8px;flex-wrap:wrap}.listen-link{padding:7px 10px;border:1px solid var(--fm-line);border-radius:7px;background:var(--fm-bg-card);font-size:12px;font-weight:500}.listen-link:hover{border-color:var(--fm-ink-soft)}
.filter-bar{display:grid;grid-template-columns:minmax(240px,1fr) minmax(170px,.25fr);gap:12px;margin-bottom:22px}.filter-bar .field{margin:0}
@media(max-width:640px){.account-action{justify-content:flex-start}}
@media(max-width:640px){.filter-bar{grid-template-columns:1fr}}
</style>
