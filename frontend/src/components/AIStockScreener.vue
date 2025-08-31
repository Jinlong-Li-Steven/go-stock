<template>
  <n-message-provider>
    <div class="ai-stock-screener">
      <n-card title="AI 智能选股工作流" :bordered="false">
        <template #header-extra>
          <n-button @click="startAnalysis" type="primary" :loading="isProcessing" :disabled="isProcessing">
            开始分析
          </n-button>
        </template>

        <n-space vertical :size="24">
          <div v-for="node in workflowNodes" :key="node.id" class="workflow-node">
            <n-card :title="node.title" size="small">
              <template #header-extra>
                <n-spin v-if="node.status === 'processing'" size="small" />
                <n-icon v-else-if="node.status === 'complete'" color="green" size="20">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z" fill="currentColor"></path></svg>
                </n-icon>
              </template>
              <div v-if="node.content" class="node-content" v-html="renderMarkdown(node.content)"></div>
              <n-empty v-else-if="node.status !== 'processing'" description="等待中..." />
              <div v-else>
                <n-space vertical>
                  <n-progress
                      type="line"
                      :percentage="node.progress"
                      :indicator-placement="'inside'"
                      processing
                  />
                  <span>{{ node.message }}</span>
                </n-space>
              </div>
            </n-card>
            <div v-if="!node.isLast" class="node-connector">
               <n-icon size="24" :depth="node.status === 'complete' ? 1 : 3">
                 <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12 4l-1.41 1.41L16.17 11H4v2h12.17l-5.58 5.59L12 20l8-8z" fill="currentColor"></path></svg>
               </n-icon>
            </div>
          </div>
        </n-space>
      </n-card>
    </div>
  </n-message-provider>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue';
import { EventsOn, EventsOff } from '../../wailsjs/runtime';
import { StartParallelAnalysis, GetAiConfigs } from '../../wailsjs/go/main/App';
import { marked } from 'marked';
import { useMessage } from 'naive-ui';

const message = useMessage();
const isProcessing = ref(false);

const workflowNodes = ref([
  { id: 'parallel_analysis', title: '1. 并行分析 (5次)', status: 'pending', content: null, isLast: false, progress: 0, message: '等待启动...' },
  { id: 'voting', title: '2. 最终投票与决策', status: 'pending', content: null, isLast: true, progress: 0, message: '等待上一步完成...' },
]);

const renderMarkdown = (content) => {
  if (!content) return '';
  return marked(content);
};

const startAnalysis = async () => {
  // Reset nodes
  workflowNodes.value.forEach(node => {
    node.status = 'pending';
    node.content = null;
    node.progress = 0;
  });
  workflowNodes.value[0].message = '等待启动...';
  workflowNodes.value[1].message = '等待上一步完成...';

  isProcessing.value = true;
  workflowNodes.value[0].status = 'processing';
  workflowNodes.value[0].message = '正在初始化并行分析...';


  try {
    const configs = await GetAiConfigs();
    const geminiConfig = configs.find(c => c.apiType === 'gemini');

    if (!geminiConfig) {
      message.error("未找到可用的 Gemini AI 配置。请先在设置页面添加一个 Gemini 类型的 AI 配置。");
      isProcessing.value = false;
      workflowNodes.value[0].status = 'pending';
      return;
    }
    
    StartParallelAnalysis(geminiConfig.ID);

  } catch (error) {
    message.error("获取 AI 配置失败: " + error);
    isProcessing.value = false;
    workflowNodes.value[0].status = 'pending';
  }
};

onMounted(() => {
  EventsOn('parallel_analysis_update', (update) => {
    const { node: nodeId, status, payload, progress, message: msg } = update;
    const targetNode = workflowNodes.value.find(n => n.id === nodeId);

    if (targetNode) {
      targetNode.status = status;

      if (progress) {
        targetNode.progress = progress;
      }
      if (msg) {
        targetNode.message = msg;
      }

      if (status === 'streaming') {
        targetNode.status = 'processing'; // Keep it processing while streaming
        if (targetNode.content === null) {
          targetNode.content = "";
        }
        targetNode.content += payload;
      } else if (payload) {
        if (targetNode.content === null) {
          targetNode.content = "";
        }
        targetNode.content += payload;
      }

      if (status === 'complete') {
        targetNode.progress = 100;
        const currentIndex = workflowNodes.value.findIndex(n => n.id === nodeId);
        if (currentIndex + 1 < workflowNodes.value.length) {
          // Start next node
          workflowNodes.value[currentIndex + 1].status = 'processing';
          workflowNodes.value[currentIndex + 1].message = '正在进行最终投票决策...';
        } else {
          // This was the last node
          isProcessing.value = false;
        }
      }
    }
  });
});

onBeforeUnmount(() => {
  EventsOff('parallel_analysis_update');
});
</script>

<style scoped>
.ai-stock-screener {
  padding: 20px;
  text-align: left;
}
.workflow-node {
  position: relative;
}
.node-connector {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 50px; /* Space between nodes */
  transform: rotate(90deg);
}
.node-content {
  white-space: pre-wrap;
  word-wrap: break-word;
}
</style>

