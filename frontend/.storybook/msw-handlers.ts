import { http, HttpResponse } from 'msw';

export const mswHandlers = {
  pipeline: [
    http.get('*/api/v1/pipelines/*', () =>
      HttpResponse.json({
        id: 'pipeline',
        name: 'Document Pipeline',
        stages: [
          { name: 'ingest', title: 'Ingest', enabled: true },
          { name: 'transcribe', title: 'Transcribe', enabled: true },
          { name: 'summarize', title: 'Summarize', enabled: true },
          { name: 'clarify', title: 'Clarify', enabled: true },
        ],
      })
    ),
  ],
  documents: [
    http.get('*/api/v1/documents', async ({ request }) => {
      const url = new URL(request.url);
      const page_size = parseInt(url.searchParams.get('page_size') || '10');
      const page_token = url.searchParams.get('page_token');
      
      const mockDocuments = [
        {
          id: 'doc-001',
          title: 'Introduction to Document Processing',
          series: 'Basics',
          current_job_id: 'job-001',
          created_at: '2024-01-15T10:30:00Z',
          updated_at: '2024-01-15T10:30:00Z',
        },
        {
          id: 'doc-002',
          title: 'Advanced Techniques Guide',
          series: 'Advanced',
          current_job_id: 'job-002',
          created_at: '2024-01-14T09:00:00Z',
          updated_at: '2024-01-14T09:00:00Z',
        },
        {
          id: 'doc-003',
          title: 'Quick Start Tutorial',
          series: 'Basics',
          current_job_id: null,
          created_at: '2024-01-13T14:20:00Z',
          updated_at: '2024-01-13T14:20:00Z',
        },
      ];

      const start = page_token ? parseInt(page_token) : 0;
      const end = start + page_size;
      const page = mockDocuments.slice(start, end);
      const nextToken = end < mockDocuments.length ? end.toString() : null;

      return HttpResponse.json({
        data: page,
        next_page_token: nextToken,
      });
    }),
  ],
  documentSeries: [
    http.get('*/api/v1/documents/series', () =>
      HttpResponse.json({
        data: ['Basics', 'Advanced', 'Reference', 'Tutorial'],
      })
    ),
  ],
  contexts: [
    http.get('*/api/v1/contexts', () =>
      HttpResponse.json({
        data: [
          {
            id: 'ctx-001',
            name: 'Project Overview',
            text: 'This is a project about document processing pipelines.',
            created_at: '2024-01-01T00:00:00Z',
          },
          {
            id: 'ctx-002',
            name: 'Technical Specifications',
            text: 'The system uses advanced NLP models for processing.',
            created_at: '2024-01-02T00:00:00Z',
          },
        ],
      })
    ),
  ],
  chats: [
    http.get('*/api/v1/chats', () =>
      HttpResponse.json({
        data: [
          {
            id: 'chat-001',
            title: 'Document Processing Questions',
            system_prompt: null,
            rag_retrieval: { enabled: true, max_sources: 5, minimum_score: 0.5 },
            created_at: '2024-04-01T10:00:00Z',
            updated_at: '2024-04-01T10:00:00Z',
          },
          {
            id: 'chat-002',
            title: 'Technical Details',
            system_prompt: 'You are a helpful assistant for technical documentation.',
            rag_retrieval: { enabled: true, max_sources: 3, minimum_score: 0.7 },
            created_at: '2024-04-01T11:30:00Z',
            updated_at: '2024-04-01T11:30:00Z',
          },
        ],
      })
    ),
  ],
  jobs: [
    http.get('*/api/v1/jobs', () =>
      HttpResponse.json({
        data: [],
      })
    ),
  ],
};
