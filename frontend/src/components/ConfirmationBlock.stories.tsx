import type { Meta, StoryObj } from '@storybook/react'
import { ConfirmationBlock } from '../components/AgentParts'

const meta = {
  component: ConfirmationBlock,
  tags: ['ai-generated'],
} satisfies Meta<typeof ConfirmationBlock>

export default meta
type Story = StoryObj<typeof meta>

const onDecideMock = () => {}

export const Pending: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-123',
      toolName: 'update_document',
      hint: 'Replace clarified_text?',
      field: 'clarified_text',
      stage: 'clarify',
      before: 'Old text',
      after: 'New text',
      status: 'pending',
    },
    onDecide: onDecideMock,
  },
}

export const Approved: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-124',
      toolName: 'update_document',
      hint: 'Update summary?',
      field: 'summary',
      stage: 'summarize',
      before: 'Old summary',
      after: 'New summary',
      status: 'approved',
    },
    onDecide: onDecideMock,
  },
}

export const Rejected: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-125',
      toolName: 'update_document',
      hint: 'Replace content?',
      field: 'content',
      stage: 'ingest',
      before: 'Original',
      after: 'Modified',
      status: 'rejected',
    },
    onDecide: onDecideMock,
  },
}
