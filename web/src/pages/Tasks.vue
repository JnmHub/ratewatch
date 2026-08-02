<template><div class="fm-page">
  <header class="fm-head"><div><span class="fm-kicker">自动同步</span><h1>同步任务</h1><p>同一个上游可以同步到多个目标分组，每个分组分别设置最低倍率和固定增减值。</p></div><FmButton variant="primary" :disabled="!sources.length||!sites.length" @click="open">新建任务</FmButton></header>
  <FmDataTable :columns="columns" :rows="tasks" :loading="loading" empty-text="暂无同步任务">
    <template #cell-name="{row}"><strong>{{row.name}}</strong><div class="muted">{{row.source_ids?.length||1}} 个上游 → {{row.site?.name}} / {{row.group?.name}}</div></template>
    <template #cell-enabled="{row}"><FmStatusPill :variant="row.enabled?'ok':'neutral'">{{row.enabled?'监听中':'已暂停'}}</FmStatusPill><div v-if="row.shadow_mode" class="muted">仅观察，不写入</div></template>
    <template #cell-minimum_upstream_rate="{value}"><span class="mono">{{value>0?fmt(value):'不限制'}}</span></template>
    <template #cell-adjustment="{value}"><span class="mono">{{value>=0?'+':''}}{{fmt(value)}}</span></template>
    <template #cell-last_target_rate="{value}"><span class="mono">{{value==null?'—':fmt(value)}}</span></template>
    <template #cell-last_status="{value}"><FmStatusPill :variant="statusVariant(value)">{{statusLabel(value)}}</FmStatusPill></template>
    <template #actions="{row}"><FmButton variant="outline" :loading="running===row.id" @click="run(row)">立即检查</FmButton><FmButton variant="ghost" @click="edit(row)">编辑</FmButton><FmButton variant="ghost" @click="toggle(row)">{{row.enabled?'暂停':'启用'}}</FmButton><FmButton variant="danger" @click="remove(row)">删除</FmButton></template>
  </FmDataTable>
  <FmModal v-model="modal" :title="editingId?'编辑同步任务':'新建同步任务'" kicker="分组独立计费" description="每个目标分组都有自己的最低倍率和固定增减值。">
    <form id="task-form" @submit.prevent="save">
      <label class="field"><span>任务名称</span><input v-model.trim="form.name" required placeholder="例如：主站默认分组"/></label>
      <label class="field"><span>选择上游</span><FmSelect v-model="primarySource" aria-label="选择上游" placeholder="请选择上游" :options="sourceOptions" /></label>
      <div class="grid-2"><label class="field"><span>目标站点</span><FmSelect v-model="form.site_id" aria-label="目标站点" placeholder="请选择目标站点" :options="siteOptions" /></label><label class="field"><span>目标分组</span><FmSelect v-model="form.group_id" aria-label="目标分组" placeholder="请选择目标分组" :options="groupOptions" /></label></div>
      <div class="grid-2">
        <label class="field"><span>最低上游倍率</span><input v-model.number="form.minimum_upstream_rate" type="number" min="0" step="0.0001"/><small>上游低于此值时，以此值为基准；填 0 表示不限制。</small></label>
        <label class="field"><span>固定增减值</span><input v-model.number="form.adjustment" type="number" step="0.0001"/><small>可填写正数加价或负数让利。</small></label>
      </div>
      <details class="advanced"><summary>高级设置</summary><div class="advanced-body">
        <fieldset><legend>更多上游（按最高有效倍率计算）</legend><label v-for="s in extraSources" :key="s.id" class="check"><input v-model="form.source_ids" type="checkbox" :value="s.id"/>{{s.name}}</label></fieldset>
        <label class="check"><input v-model="form.shadow_mode" type="checkbox"/>只观察变化，不写入目标站点</label>
        <label class="field"><span>较大变化提醒阈值</span><input v-model.number="form.large_change_percent" type="number" min="1" max="1000" step="1"/><small>达到该百分比时会额外提醒，但默认仍立即同步。</small></label>
      </div></details>
      <div class="notice">计算规则：max（实际上游倍率，最低上游倍率）＋固定增减值。结果小于或等于 0 时停止写入。</div><div v-if="error" class="notice error">{{error}}</div>
    </form>
    <template #actions><FmButton variant="ghost" @click="modal=false">取消</FmButton><FmButton variant="primary" type="submit" form="task-form" :loading="saving" :disabled="!primarySource||!form.site_id||!form.group_id">{{editingId?'保存设置':'创建并开始监听'}}</FmButton></template>
  </FmModal>
</div></template>
<script setup>
import{computed,onMounted,reactive,ref,watch}from"vue";import{api,toast}from"../api/client.js";import FmButton from"../ui/freemodel/components/FmButton.vue";import FmDataTable from"../ui/freemodel/components/FmDataTable.vue";import FmModal from"../ui/freemodel/components/FmModal.vue";import FmSelect from"../ui/freemodel/components/FmSelect.vue";import FmStatusPill from"../ui/freemodel/components/FmStatusPill.vue";
const emptyForm=()=>({name:"",source_ids:[],site_id:0,group_id:0,minimum_upstream_rate:0,adjustment:0,shadow_mode:false,large_change_percent:50});
const tasks=ref([]),sources=ref([]),sites=ref([]),inventory=reactive({}),loading=ref(true),modal=ref(false),saving=ref(false),running=ref(0),error=ref(""),primarySource=ref(0),editingId=ref(0),form=reactive(emptyForm());
const groups=computed(()=>inventory[form.site_id]||[]),extraSources=computed(()=>sources.value.filter(s=>s.id!==primarySource.value)),sourceOptions=computed(()=>sources.value.map(s=>({value:s.id,label:`${s.name} · ${s.last_rate??'等待首次检查'}`}))),siteOptions=computed(()=>sites.value.map(s=>({value:s.id,label:s.name}))),groupOptions=computed(()=>groups.value.map(g=>({value:g.id,label:`${g.name} · ${fmt(g.rate)}`}))),columns=[{key:"name",label:"任务"},{key:"enabled",label:"状态"},{key:"minimum_upstream_rate",label:"最低上游倍率"},{key:"adjustment",label:"固定增减值"},{key:"last_target_rate",label:"目标倍率"},{key:"last_status",label:"最近结果"}];
watch(()=>form.site_id,()=>{if(form.group_id&&!groups.value.some(g=>g.id===form.group_id))form.group_id=0});watch(primarySource,(next,old)=>{form.source_ids=form.source_ids.filter(id=>id!==old);if(next&&!form.source_ids.includes(next))form.source_ids.unshift(next)});
function open(){editingId.value=0;primarySource.value=0;Object.assign(form,emptyForm());modal.value=true;error.value=""}
function edit(task){const ids=[...(task.source_ids?.length?task.source_ids:[task.source_id])];editingId.value=task.id;Object.assign(form,{name:task.name,source_ids:ids,site_id:task.site_id,group_id:task.group_id,minimum_upstream_rate:task.minimum_upstream_rate||0,adjustment:task.adjustment,shadow_mode:task.shadow_mode,large_change_percent:task.large_change_percent||50});primarySource.value=ids[0]||0;modal.value=true;error.value=""}
async function load(){loading.value=true;try{[tasks.value,sources.value,sites.value]=await Promise.all([api("/api/tasks"),api("/api/sources"),api("/api/sites")]);await Promise.all(sites.value.map(async s=>inventory[s.id]=await api(`/api/sites/${s.id}/inventory`)))}catch(e){toast(e.message,"error")}finally{loading.value=false}}
async function save(){saving.value=true;error.value="";try{const updating=editingId.value>0;await api(updating?`/api/tasks/${editingId.value}`:"/api/tasks",{method:updating?"PUT":"POST",body:JSON.stringify({...form,enabled:true})});toast(updating?"同步任务设置已保存":"同步任务已创建");modal.value=false;editingId.value=0;primarySource.value=0;Object.assign(form,emptyForm());load()}catch(e){error.value=e.message;toast(e.message,"error")}finally{saving.value=false}}
async function run(t){running.value=t.id;try{await api(`/api/tasks/${t.id}/run`,{method:"POST"});toast("检查完成，已按规则处理");load()}catch(e){toast(e.message,"error");load()}finally{running.value=0}}
async function toggle(t){try{await api(`/api/tasks/${t.id}/enabled`,{method:"PUT",body:JSON.stringify({enabled:!t.enabled})});toast(t.enabled?"任务已暂停":"任务已启用");load()}catch(e){toast(e.message,"error")}}
async function remove(t){if(!confirm(`确认删除任务“${t.name}”？`))return;try{await api(`/api/tasks/${t.id}`,{method:"DELETE"});toast("任务已删除");load()}catch(e){toast(e.message,"error")}}
const fmt=v=>Number(v??0).toFixed(4).replace(/0+$/,"").replace(/\.$/,""),statusLabel=v=>({pending:"等待首次检查",ok:"正常",failed:"失败",blocked:"已阻止",observed:"发现变化"}[v]||v),statusVariant=v=>({ok:"ok",failed:"error",blocked:"warn",observed:"info",pending:"neutral"}[v]||"neutral");onMounted(load);
</script><style scoped>fieldset{display:grid;gap:8px;margin:0 0 16px;padding:14px;border:1px solid var(--fm-line);border-radius:10px}legend{padding:0 6px;color:var(--fm-ink-muted);font-size:10px;letter-spacing:.08em;text-transform:uppercase}</style>
