import type { Meta, StoryObj } from '@storybook/react'
import { ConfirmationBlock } from './AgentParts'

const onDecideMock = () => {}

/**
 * ConfirmationBlock renders the inline approval card for a tool-call that
 * requested human-in-the-loop confirmation. Shows the diff of the proposed
 * change, an editable "after" textarea pre-filled with the model's proposal
 * (user can tweak before approving), and Approve / Reject buttons.
 *
 * The "after" textarea state lives in this component because the user may
 * edit it any number of times before deciding; we only push the value up
 * when they click Approve.
 *
 * @summary inline approval card for tool-call confirmations
 */
const meta = {
  title: 'Components/ConfirmationBlock',
  component: ConfirmationBlock,
  parameters: {
    layout: 'centered',
  },
  tags: ['autodocs', 'manifest'],
  argTypes: {
    part: {
      control: false,
      description: 'The confirmation part with all required fields',
    },
    onDecide: {
      action: 'decide',
      description: 'Callback when user approves or rejects',
    },
  },
} satisfies Meta<typeof ConfirmationBlock>

export default meta
type Story = StoryObj<typeof meta>

/**
 * Pending confirmation with a text diff. Shows the default Diff tab view.
 *
 * @summary pending confirmation with text diff
 */
export const Pending: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-123',
      toolName: 'update_document',
      hint: 'Replace clarified_text for document "intro.md"?',
      field: 'clarified_text',
      stage: 'clarify',
      before: `# Introduction

This is the old introduction that needs to be replaced.`,
      after: `# Introduction

This is the new improved introduction with better details.`,
      status: 'pending',
    },
    onDecide: onDecideMock,
  },
}

/**
 * Pending confirmation with markdown content in the editable tab.
 *
 * @summary pending confirmation with markdown content
 */
export const PendingWithMarkdown: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-124',
      toolName: 'update_document',
      hint: 'Update summary for report.pdf?',
      field: 'summary',
      stage: 'summarize',
      before: `# Report Summary

Old summary goes here with less detail.`,
      after: `# Report Summary

## Key Findings

- Major finding one
- Major finding two
- Major finding three

## Recommendations

1. Implement change A
2. Implement change B
3. Schedule follow-up review`,
      status: 'pending',
    },
    onDecide: onDecideMock,
  },
}

/**
 * Approved confirmation - collapsed state shows green status indicator.
 *
 * @summary approved confirmation (collapsed)
 */
export const Approved: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-125',
      toolName: 'update_document',
      hint: 'Replace clarified_text for document "data.md"?',
      field: 'clarified_text',
      stage: 'clarify',
      before: `Data section with old values.

Value A: 100
Value B: 200`,
      after: `Data section with updated values.

Value A: 150
Value B: 250`,
      status: 'approved',
    },
    onDecide: onDecideMock,
  },
}

/**
 * Rejected confirmation - collapsed state shows red status indicator.
 *
 * @summary rejected confirmation (collapsed)
 */
export const Rejected: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-126',
      toolName: 'update_document',
      hint: 'Update narrative_summary for article.txt?',
      field: 'narrative_summary',
      stage: 'narrative',
      before: `The article discusses the history of the company.

It starts with founding in 2010.

Then covers growth through 2020.`,
      after: `The article discusses the company's history and evolution.

It begins with its founding in 2010.

Then traces its growth through 2020 and beyond.`,
      status: 'rejected',
    },
    onDecide: onDecideMock,
  },
}

/**
 * Shows the Editable tab where users can modify the proposal before approving.
 *
 * @summary editing confirmation before approving
 */
export const EditingBeforeApproving: Story = {
  args: {
    part: {
      kind: 'confirmation',
      callId: 'call-127',
      toolName: 'update_document',
      hint: 'Replace clarified_text?',
      field: 'clarified_text',
      stage: 'clarify',
      before: `Original content.

This is the baseline.`,
      after: `Proposed new content.

This is the model's suggestion.`,
      status: 'pending',
    },
    onDecide: onDecideMock,
  },
}
