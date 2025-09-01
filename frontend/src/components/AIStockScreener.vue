<template>
  <n-message-provider>
    <div class="ai-stock-screener">
      <n-card title="AI 智能选股工作流" :bordered="false">
        <template #header-extra>
          <n-button @click="startAnalysis" type="primary" :loading="isProcessing" :disabled="isProcessing">
            {{ isProcessing ? '分析中...' : '开始分析' }}
          </n-button>
        </template>

        <!-- Pipelines Display -->
        <div class="pipelines-container">
          <div v-for="pipeline in pipelines" :key="pipeline.id" class="pipeline">
            <div class="pipeline-header">流水线 {{ pipeline.id }}</div>
            <div v-for="(node, index) in pipeline.nodes" :key="node.id" class="node-wrapper">
              <div class="node" :class="['status-' + node.status]">
                <n-spin v-if="node.status === 'processing'" size="small" />
                <n-icon v-else size="20" :color="getNodeIconColor(node.status)">
                  <component :is="getNodeIcon(node.status)" />
                </n-icon>
                <span class="node-title">{{ node.title }}</span>
              </div>
              <div v-if="index < pipeline.nodes.length - 1" class="connector" :class="{ active: node.status === 'complete' }"></div>
            </div>
          </div>
        </div>

        <!-- Final Report Display -->
        <n-card title="最终输出" :bordered="true" style="margin-top: 24px;">
           <div v-if="finalReport.status === 'processing' || finalReport.content" class="final-report-content" v-html="renderMarkdown(finalReport.content)"></div>
           <n-empty v-else-if="finalReport.status === 'pending'" description="等待所有流水线分析完成..." />
           <div v-else-if="finalReport.status === 'error'">
             <n-alert title="生成最终报告时出错" type="error">
               {{ finalReport.content }}
             </n-alert>
           </div>
        </n-card>

      </n-card>
    </div>
  </n-message-provider>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, h } from 'vue';
import { EventsOn, EventsOff } from '../../wailsjs/runtime';
import { StartAIStockScreenerStream, GetAiConfigs } from '../../wailsjs/go/main/App';
import { marked } from 'marked';
import { useMessage } from 'naive-ui';
import { CheckmarkCircleOutline, CloseCircleOutline, EllipsisHorizontalCircleOutline, HourglassOutline } from '@vicons/ionicons5';

const message = useMessage();
const isProcessing = ref(false);
const pipelines = ref([]);
const finalReport = ref({ status: 'pending', content: '' });

const NODE_TITLES = {
  'data_gathering': '数据收集',
  'event_analysis': '事件分析',
  'technical_analysis': '技术分析',
  'final_report_flow': '生成报告'
};

const initializePipelines = () => {
  pipelines.value = Array.from({ length: 5 }, (_, i) => ({
    id: i + 1,
    nodes: [
      { id: 'data_gathering', title: '数据收集', status: 'pending' },
      { id: 'event_analysis', title: '事件分析', status: 'pending' },
      { id: 'technical_analysis', title: '技术分析', status: 'pending' },
      { id: 'final_report_flow', title: '生成报告', status: 'pending' }
    ]
  }));
};

const getNodeIcon = (status) => {
  switch (status) {
    case 'complete':
      return h(CheckmarkCircleOutline);
    case 'error':
      return h(CloseCircleOutline);
    case 'processing':
       return h(EllipsisHorizontalCircleOutline);
    default:
      return h(HourglassOutline);
  }
};

const getNodeIconColor = (status) => {
  switch (status) {
    case 'complete':
      return '#63e2b7'; // success color
    case 'error':
      return '#e88080'; // error color
    case 'processing':
      return '#808080';
    default:
      return '#d3d3d3'; // pending color
  }
};

const startAnalysis = async () => {
  initializePipelines();
  finalReport.value = { status: 'pending', content: '' };
  isProcessing.value = true;

  try {
    const configs = await GetAiConfigs();
    const geminiConfig = configs.find(c => c.apiType === 'gemini');

    if (!geminiConfig) {
      message.error("未找到可用的 Gemini AI 配置。");
      isProcessing.value = false;
      return;
    }
    
    StartAIStockScreenerStream(geminiConfig.ID);

  } catch (error) {
    message.error("获取 AI 配置失败: " + error);
    isProcessing.value = false;
  }
};

const renderMarkdown = (content) => {
  if (!content) return '';
  return marked(content);
};

onMounted(() => {
  initializePipelines(); // Initial setup
  EventsOn('ai_screener_update', (update) => {
    const { flow_id, node: nodeId, status, payload } = update;

    if (nodeId === 'final_report') {
      finalReport.value.status = status;
      if (status === 'streaming') {
        finalReport.value.status = 'processing';
        finalReport.value.content += payload;
      } else if (status === 'complete') {
        isProcessing.value = false;
      } else if (status === 'error') {
        finalReport.value.content = payload;
        isProcessing.value = false;
      }
    } else if (flow_id >= 1 && flow_id <= 5) {
      const pipeline = pipelines.value[flow_id - 1];
      if (pipeline) {
        if (nodeId === 'flow_status' && status === 'error') {
            // Mark all pending nodes in this pipeline as error
            pipeline.nodes.forEach(node => {
                if(node.status === 'pending' || node.status === 'processing'){
                    node.status = 'error';
                }
            });
            message.error(`流水线 ${flow_id} 失败: ${payload}`);
        } else {
            const node = pipeline.nodes.find(n => n.id === nodeId);
            if (node) {
                node.status = status;
            }
        }
      }
    }
  });
});

onBeforeUnmount(() => {
  EventsOff('ai_screener_update');
});
</script>

<style scoped>
.ai-stock-screener {
  padding: 20px;
  text-align: left;
}
.pipelines-container {
  display: flex;
  justify-content: space-around;
  gap: 20px;
}
.pipeline {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 18%;
}
.pipeline-header {
  font-weight: bold;
  margin-bottom: 16px;
}
.node-wrapper {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 100%;
}
.node {
  display: flex;
  align-items: center;
  padding: 8px 12px;
  border-radius: 8px;
  width: 100%;
  transition: all 0.3s ease;
  border: 1px solid #ccc;
}
.node-title {
  margin-left: 8px;
  font-size: 14px;
}
.connector {
  width: 2px;
  height: 30px;
  background-color: #ccc;
  transition: background-color 0.3s ease;
}
.connector.active {
  background-color: #63e2b7;
}

/* Node Status Styles */
.status-pending {
  background-color: #f0f0f0;
  border-color: #dcdcdc;
  color: #888;
}
.status-processing {
  background-color: #e6f7ff;
  border-color: #91d5ff;
  color: #555;
}
.status-complete {
  background-color: #f6ffed;
  border-color: #b7eb8f;
  color: #333;
}
.status-error {
  background-color: #fff1f0;
  border-color: #ffa39e;
  color: #d4380d;
}

.final-report-content {
  white-space: pre-wrap;
  word-wrap: break-word;
  max-height: 600px;
  overflow-y: auto;
}
</style>