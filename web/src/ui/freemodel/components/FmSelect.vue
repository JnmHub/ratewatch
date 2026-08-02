<template>
  <div ref="root" class="fm-select" :class="{open,disabled}">
    <button class="fm-select-trigger" type="button" :disabled="disabled" :aria-label="ariaLabel" aria-haspopup="listbox" :aria-expanded="open" @click="toggle" @keydown="onTriggerKey">
      <span :class="{placeholder:!selected}">{{ selected?.label || placeholder }}</span>
      <span class="fm-select-chevron" aria-hidden="true"><i/><i/></span>
    </button>
    <Transition name="select-pop">
      <div v-if="open" class="fm-select-menu" role="listbox" :aria-label="ariaLabel">
        <button v-for="option in options" :key="String(option.value)" type="button" role="option" class="fm-select-option" :class="{selected:isSelected(option),disabled:option.disabled}" :aria-selected="isSelected(option)" :disabled="option.disabled" @click="choose(option)">
          <span>{{ option.label }}</span><span v-if="isSelected(option)" class="fm-select-check" aria-hidden="true">✓</span>
        </button>
      </div>
    </Transition>
  </div>
</template>
<script setup>
import{computed,nextTick,onBeforeUnmount,onMounted,ref}from"vue";
const props=defineProps({modelValue:{type:[String,Number,Boolean],default:""},options:{type:Array,default:()=>[]},placeholder:{type:String,default:"请选择"},ariaLabel:{type:String,default:"选择"},disabled:Boolean});
const emit=defineEmits(["update:modelValue","change"]),root=ref(null),open=ref(false);
const selected=computed(()=>props.options.find(option=>Object.is(option.value,props.modelValue)));
function isSelected(option){return Object.is(option.value,props.modelValue)}
function toggle(){if(!props.disabled)open.value=!open.value}
function choose(option){if(option.disabled)return;emit("update:modelValue",option.value);emit("change",option.value);open.value=false}
function closeOutside(event){if(!root.value?.contains(event.target))open.value=false}
function onTriggerKey(event){if(["ArrowDown","Enter"," "].includes(event.key)){event.preventDefault();open.value=true;nextTick(()=>root.value?.querySelector('.fm-select-option:not(:disabled)')?.focus())}if(event.key==="Escape")open.value=false}
function onDocumentKey(event){if(event.key==="Escape")open.value=false}
onMounted(()=>{document.addEventListener("pointerdown",closeOutside);document.addEventListener("keydown",onDocumentKey)});onBeforeUnmount(()=>{document.removeEventListener("pointerdown",closeOutside);document.removeEventListener("keydown",onDocumentKey)});
</script>
<style scoped>
.fm-select{position:relative;width:100%;min-width:0}.fm-select-trigger{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%;min-height:44px;padding:10px 13px;border:1px solid var(--fm-line);border-radius:10px;background:var(--fm-bg-card);color:var(--fm-ink);text-align:left;transition:border-color .16s,box-shadow .16s,background .16s}.fm-select-trigger:hover{border-color:color-mix(in srgb,var(--fm-ink-muted) 55%,var(--fm-line))}.fm-select.open .fm-select-trigger{border-color:var(--fm-accent);box-shadow:0 0 0 3px color-mix(in srgb,var(--fm-accent) 14%,transparent)}.fm-select-trigger .placeholder{color:var(--fm-ink-muted)}.fm-select-chevron{position:relative;flex:0 0 18px;width:18px;height:18px;border-radius:5px;background:var(--fm-bg-warm)}.fm-select-chevron i{position:absolute;top:8px;width:6px;height:1.5px;border-radius:2px;background:var(--fm-ink-muted);transition:transform .18s}.fm-select-chevron i:first-child{left:4px;transform:rotate(45deg)}.fm-select-chevron i:last-child{right:4px;transform:rotate(-45deg)}.fm-select.open .fm-select-chevron i:first-child{transform:rotate(-45deg)}.fm-select.open .fm-select-chevron i:last-child{transform:rotate(45deg)}.fm-select-menu{position:absolute;z-index:80;top:calc(100% + 7px);left:0;right:0;display:grid;gap:3px;max-height:260px;padding:6px;border:1px solid var(--fm-line);border-radius:11px;background:var(--fm-bg-card);box-shadow:0 18px 45px rgba(0,0,0,.16);overflow:auto}.fm-select-option{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%;padding:9px 10px;border-radius:7px;background:transparent;color:var(--fm-ink-soft);text-align:left;font-size:13px}.fm-select-option:hover,.fm-select-option:focus-visible{background:var(--fm-bg-warm);color:var(--fm-ink)}.fm-select-option.selected{background:var(--fm-accent-bg);color:var(--fm-accent-ink);font-weight:600}.fm-select-option.disabled{opacity:.45;cursor:not-allowed}.fm-select-check{display:grid;place-items:center;width:18px;height:18px;border-radius:50%;background:var(--fm-accent);color:white;font-size:11px}.fm-select.disabled{opacity:.58}.select-pop-enter-active,.select-pop-leave-active{transition:opacity .14s,transform .14s}.select-pop-enter-from,.select-pop-leave-to{opacity:0;transform:translateY(-4px) scale(.985)}
</style>
