import { createApp } from "vue";
import { createRouter,createWebHistory } from "vue-router";
import App from "./App.vue";
import Overview from "./pages/Overview.vue";
import Sites from "./pages/Sites.vue";
import Sources from "./pages/Sources.vue";
import Tasks from "./pages/Tasks.vue";
import Events from "./pages/Events.vue";
import Settings from "./pages/Settings.vue";
import Profile from "./pages/Profile.vue";
import About from "./pages/About.vue";
import Admin from "./pages/Admin.vue";
import ResetPassword from "./pages/ResetPassword.vue";
import "./ui/freemodel/styles/base.css";
import { initTheme } from "./ui/freemodel/theme.js";
initTheme();
const routes=[
  {path:"/",component:Overview},{path:"/sites",component:Sites},{path:"/sources",component:Sources},{path:"/tasks",component:Tasks},{path:"/events",component:Events},{path:"/settings",component:Settings},{path:"/profile",component:Profile},{path:"/about",component:About},{path:"/reset-password",component:ResetPassword},{path:"/:adminPath",component:Admin}
];
const router=createRouter({history:createWebHistory(),routes});
createApp(App).use(router).mount("#app");
