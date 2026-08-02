<template>
  <div class="fm-page">
    <header class="fm-head"><div><span class="fm-kicker">PREFERENCES / 租户设置</span><h1>通知与界面</h1><p>平台所有者统一配置发件邮箱；每位用户只设置自己的收件地址和通知类型。</p></div></header>
    <div class="grid-2">
      <section class="fm-card">
        <h2>邮件摘要</h2><p class="muted">默认每 10 分钟合并一次变更摘要。网页通知仍然实时推送。</p>
        <form @submit.prevent="save">
          <label class="field"><span>收件邮箱</span><input v-model.trim="form.notify_email" type="email" :disabled="!form.email_enabled" /></label>
          <label class="check email-toggle"><input v-model="form.email_enabled" type="checkbox" />启用邮件摘要</label>
          <fieldset v-if="form.email_enabled"><legend>邮件事件</legend><label v-for="k in kinds" :key="k.value" class="check"><input v-model="form.notify_kinds" type="checkbox" :value="k.value" />{{ k.label }}</label></fieldset>
          <div v-if="message" class="notice" :class="{ error: failed, ok: !failed }">{{ message }}</div>
          <FmButton variant="primary" type="submit" :loading="saving">保存通知设置</FmButton>
        </form>
      </section>
      <section class="fm-card">
        <h2>界面主题</h2><p class="muted">亮暗主题保持同一信息层级，跟随系统会响应设备偏好。</p>
        <label class="field"><span>主题模式</span><FmSelect v-model="theme" aria-label="主题模式" :options="themeOptions" @change="changeTheme" /></label>
        <div class="theme-sample"><span/><span/><span/></div>
      </section>
    </div>
    <section class="section fm-card soft"><span class="fm-kicker">SECURITY</span><h2>凭证安全边界</h2><p>管理员 Key 和普通上游 Key 使用服务端主密钥进行 AES-256-GCM 加密。页面不提供明文回显；目标站点未导出已有账号 Key 时，需要用户手动补填一次。系统不尝试绕过二次验证，也不安装伴生插件。</p></section>
  </div>
</template>
<script setup>
import { onMounted, reactive, ref } from "vue";
import { api, session, toast } from "../api/client.js";
import { readThemeMode, setThemeMode } from "../ui/freemodel/theme.js";
import FmButton from "../ui/freemodel/components/FmButton.vue";
import FmSelect from "../ui/freemodel/components/FmSelect.vue";
const defaults=["rate_changed","write_failed","probe_failed","partial_probe","invalid_rate","model_diff","image_observed"];
const kinds=[{value:"rate_changed",label:"倍率修改成功"},{value:"write_failed",label:"目标写入失败"},{value:"probe_failed",label:"上游探测失败"},{value:"partial_probe",label:"部分上游被忽略"},{value:"invalid_rate",label:"计算结果无效"},{value:"model_diff",label:"模型列表变化"},{value:"image_observed",label:"生图价格观测"}];
const form=reactive({notify_email:"",email_enabled:true,notify_kinds:[...defaults]}),saving=ref(false),message=ref(""),failed=ref(false),theme=ref(readThemeMode());
const themeOptions=[{value:"system",label:"跟随电脑"},{value:"light",label:"亮色"},{value:"dark",label:"暗色"}];
onMounted(()=>Object.assign(form,{notify_email:session.user.notify_email,email_enabled:session.user.email_enabled,notify_kinds:session.user.notify_kinds?.length?session.user.notify_kinds:[...defaults]}));
function changeTheme(){setThemeMode(theme.value)}
async function save(){saving.value=true;message.value="";try{session.user=await api("/api/me/notifications",{method:"PUT",body:JSON.stringify(form)});message.value="通知设置已保存";failed.value=false;toast(message.value,"success")}catch(e){message.value=e.message;failed.value=true;toast(e.message,"error")}finally{saving.value=false}}
</script>
<style scoped>
.fm-card h2{margin:6px 0;font-size:19px}.fm-card form{margin-top:22px}fieldset{display:grid;gap:8px;margin:16px 0;padding:14px;border:1px solid var(--fm-line);border-radius:10px}legend{padding:0 6px;color:var(--fm-ink-muted);font-size:10px;letter-spacing:.08em;text-transform:uppercase}.theme-sample{display:grid;grid-template-columns:2fr 1fr 1fr;gap:6px;margin-top:20px;padding:18px;border:1px solid var(--fm-line);border-radius:10px;background:var(--fm-bg-warm)}.theme-sample span{height:55px;border:1px solid var(--fm-line);border-radius:7px;background:var(--fm-bg-card)}.theme-sample span:nth-child(2){background:var(--fm-accent-bg)}.theme-sample span:nth-child(3){background:var(--fm-ink)}.section p{max-width:820px;color:var(--fm-ink-soft);font-size:13px}
.email-toggle{margin:4px 0 22px}
</style>
