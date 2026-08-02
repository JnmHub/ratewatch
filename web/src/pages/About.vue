<template>
  <div class="fm-page about-page">
    <header class="fm-head">
      <div><span class="fm-kicker">项目信息</span><h1>关于 RateWatch</h1><p>一个面向 New API 与 Sub2API 站点的开源倍率同步与利润保护工具。</p></div>
      <a :href="repositoryUrl" target="_blank" rel="noopener noreferrer"><FmButton variant="primary"><FolderGit2 :size="16" aria-hidden="true"/>查看 GitHub 仓库</FmButton></a>
    </header>

    <section class="project-card fm-card">
      <div class="project-mark">R</div>
      <div class="project-copy"><FmStatusPill variant="ok">开源项目</FmStatusPill><h2>让上游调价不再悄悄吞掉利润</h2><p>RateWatch 使用普通 API Key 读取实际生效倍率，再按每个目标分组独立设置的最低倍率和固定增减值，单向同步到你拥有管理员权限的站点。</p></div>
    </section>

    <div class="info-grid">
      <article class="fm-card info-card"><FolderGit2 :size="20" aria-hidden="true"/><span class="label">开源仓库</span><strong>JnmHub / ratewatch</strong><a :href="repositoryUrl" target="_blank" rel="noopener noreferrer">{{ repositoryUrl }}<ExternalLink :size="13" aria-hidden="true"/></a></article>
      <article class="fm-card info-card"><Scale :size="20" aria-hidden="true"/><span class="label">开源协议</span><strong>MIT License</strong><p>允许使用、修改和分发，并保留原始版权与许可声明。</p></article>
      <article class="fm-card info-card"><Code2 :size="20" aria-hidden="true"/><span class="label">技术栈</span><strong>Go · Vue 3 · SQLite</strong><p>单体部署，支持 Docker Compose、亮暗主题和多租户隔离。</p></article>
      <article class="fm-card info-card"><ShieldCheck :size="20" aria-hidden="true"/><span class="label">安全边界</span><strong>只允许单向同步</strong><p>仅执行“上游 → 目标”倍率同步，不提供反向写入或跨租户共享。</p></article>
    </div>

    <section class="section repository-section">
      <div class="section-head"><div><h2>参与项目</h2><p>欢迎查看源码、提交问题或改进代码。</p></div></div>
      <div class="repository-actions fm-card">
        <div><strong>GitHub 公开仓库</strong><p class="mono">github.com/JnmHub/ratewatch</p></div>
        <div class="fm-actions"><a :href="`${repositoryUrl}/issues`" target="_blank" rel="noopener noreferrer"><FmButton variant="outline">提交问题<ExternalLink :size="14" aria-hidden="true"/></FmButton></a><a :href="repositoryUrl" target="_blank" rel="noopener noreferrer"><FmButton variant="primary">浏览源码<FolderGit2 :size="14" aria-hidden="true"/></FmButton></a></div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { Code2, ExternalLink, FolderGit2, Scale, ShieldCheck } from "@lucide/vue";
import FmButton from "../ui/freemodel/components/FmButton.vue";
import FmStatusPill from "../ui/freemodel/components/FmStatusPill.vue";

const repositoryUrl = "https://github.com/JnmHub/ratewatch";
</script>

<style scoped>
.about-page{max-width:1180px}.project-card{display:grid;grid-template-columns:auto minmax(0,1fr);align-items:center;gap:22px;padding:26px}.project-mark{display:grid;place-items:center;width:62px;height:62px;border-radius:14px;background:var(--fm-ink);color:var(--fm-bg);font-size:26px;font-weight:800}.project-copy h2{margin:12px 0 7px;font-size:22px}.project-copy p{max-width:780px;margin:0;color:var(--fm-ink-soft);font-size:14px;line-height:1.65}.info-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px;margin-top:14px}.info-card{display:grid;align-content:start;gap:8px;min-height:170px}.info-card>svg{color:var(--fm-accent-ink)}.info-card strong{font-size:17px}.info-card p,.info-card a{margin:0;color:var(--fm-ink-muted);font-size:12px;line-height:1.6}.info-card a{display:flex;align-items:center;gap:6px;overflow-wrap:anywhere}.repository-actions{display:flex;align-items:center;justify-content:space-between;gap:18px}.repository-actions p{margin:5px 0 0;color:var(--fm-ink-muted);font-size:12px}.repository-actions a{display:inline-flex}.button svg{flex:0 0 auto}@media(max-width:720px){.info-grid{grid-template-columns:1fr}.repository-actions{align-items:stretch;flex-direction:column}.repository-actions .fm-actions{display:grid;grid-template-columns:1fr}.repository-actions a,.repository-actions .button{width:100%}}@media(max-width:480px){.project-card{grid-template-columns:1fr}.project-mark{width:52px;height:52px}.project-copy h2{font-size:19px}}
</style>
