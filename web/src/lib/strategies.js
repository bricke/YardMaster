// What each Switchyard routing strategy does, in plain words. The setup wizard's help
// dialog and the Help page both read this file, so they never disagree.
//
// Written from Switchyard v0.3.0's docs (docs/routing_algorithms/*.md) and behaviour
// measured on real deployments. Re-check it when upgrading Switchyard.

export const SWITCHYARD_VERSION = 'v0.3.0'

export const strategies = [
  {
    type: 'passthrough',
    name: 'Passthrough',
    wizard: true,
    summary: 'Every request goes to one model. No decision is made.',
    how: [
      'The route is simply a name for one model. Switchyard forwards each request to it, retrying on timeouts, rate limits and server errors.',
    ],
    models: 'One',
    extraCalls: 'None',
    latency: 'None added',
    goodFor: [
      'Giving coworkers a stable name for a model, so you can swap the model later without touching their tools',
      'Tracking one model’s usage on its own',
    ],
    avoidWhen: ['You want to save money on easy tasks: nothing is routed'],
    settings: [{ name: 'Model', explain: 'The model that answers every request.' }],
  },
  {
    type: 'random',
    name: 'Random',
    wizard: true,
    summary: 'Splits traffic between models by weight, without looking at the request.',
    how: [
      'For each request, Switchyard picks one of the route’s models at random, in proportion to the weights. Weights 3 and 7 mean about 30% and 70%.',
      'Each pick is independent, so short runs won’t match the split exactly.',
    ],
    models: 'Two or more',
    extraCalls: 'None',
    latency: 'None added',
    goodFor: ['A/B comparisons between models', 'Gradually moving traffic to a new model', 'Baselines for cost experiments'],
    avoidWhen: ['The request itself should decide the model: hard tasks can land on the weak model'],
    settings: [
      { name: 'Models and weights', explain: 'Relative shares; they don’t need to add up to anything. A weight of 0 disables a model.', start: 'Equal weights' },
    ],
  },
  {
    type: 'auto',
    name: 'Auto',
    wizard: true,
    summary: 'Switchyard’s recommended default: starts on the efficient model and moves to the capable one when the work gets hard.',
    how: [
      'Auto is a preset of the Stage router: efficient-first, confidence 0.5, no judge. Nothing extra is called to decide.',
      'It reads the tool results in the conversation (files read or edited, commands run, tests passing or failing) to estimate where an agent is in its work. Settled, mechanical steps stay on the efficient model; errors, repeated failures, spinning and exploration move to the capable one. After escalating it stays on the capable model for a couple of turns.',
      'A request with no tool history (a plain chat question) has nothing to read, so it goes to the efficient model.',
    ],
    models: 'Two: capable and efficient',
    extraCalls: 'None',
    latency: 'Negligible',
    goodFor: ['Coding agents (Claude Code, Codex, OpenCode…): the signals are tuned for them', 'Saving money with no judge to run or pay for'],
    avoidWhen: ['Plain chat without tools: every request goes to the efficient model, however hard'],
    settings: [
      { name: 'Capable model', explain: 'Used for exploration, error recovery and hard reasoning.' },
      { name: 'Efficient model', explain: 'Used for routine steps and whenever the signals aren’t sure.' },
    ],
  },
  {
    type: 'llm_classifier',
    name: 'Classifier (capability)',
    wizard: true,
    summary: 'A judge model reads the task and estimates whether the weak model can solve it.',
    how: [
      'Before answering, Switchyard asks the judge model for a verdict: the probability that the weak model completes the task (p_solve), and whether the task is inside or outside the weak model’s abilities.',
      'If p_solve is at or above the threshold, the weak model answers; otherwise the strong one does. Verdicts that are uncertain or unsupported raise the bar by the threshold step. A verdict the judge can’t produce goes to the strong model.',
      'If the call to the judge itself fails (a timeout, or the provider refusing the request), the request fails, on every route that uses that judge.',
    ],
    models: 'Three roles: strong, weak and a judge (the judge can be one of the other two)',
    extraCalls: 'One judge call per decision',
    latency: 'The judge’s response time on every decision: about 2–4 s with a fast judge, much more with a reasoning model',
    goodFor: ['Chat and single questions, where the request text says how hard the task is', 'Teams mixing easy and hard questions on one route'],
    avoidWhen: [
      'Coding agents with “every request”: the judge runs on each tool step. Use “each new user message” or Auto',
      'Your only judge candidate is a slow reasoning model',
    ],
    settings: [
      { name: 'Strong / weak model', explain: 'Hard tasks go to the strong model, easy ones to the weak model.' },
      { name: 'Judge model', explain: 'Decides; never answers. See “Choosing a judge”.' },
      {
        name: 'Threshold (0–1)',
        explain: 'Lowest estimated chance of success that still goes to the weak model. Higher sends more to the strong model. Judges differ: a lenient judge needs a higher threshold.',
        start: '0.5; around 0.7 with a lenient judge',
      },
      {
        name: 'Judge runs',
        explain: '“Every request” judges each call, tool steps included. “Each new user message” judges when the person writes and keeps the choice through the tool steps that follow. “Once per session” judges the first message only. The last two need the tool to send a session ID (Claude Code, Codex and OpenCode do).',
        start: 'Each new user message',
      },
    ],
  },
  {
    type: 'stage_router',
    name: 'Stage router',
    wizard: false,
    summary: 'Auto’s underlying algorithm, with all its settings exposed.',
    how: [
      'Scores each turn from the tool-result history: errors, spinning and exploration push toward the capable model; steady edits push toward the efficient one. Only a confident score overrides the default tier.',
      'Optionally, a judge decides the turns the signals aren’t sure about.',
    ],
    models: 'Two, plus an optional judge',
    extraCalls: 'None, or a judge call on unsure turns',
    latency: 'Negligible without a judge',
    goodFor: ['Tuning Auto: which tier comes first, how confident the signals must be, how long to stay escalated', 'Agents with their own tool names (tool_semantics)'],
    avoidWhen: ['You don’t need to tune it: use Auto'],
    settings: [
      { name: 'picker', explain: 'efficient_first or capable_first: the tier used when the signals aren’t sure.' },
      { name: 'confidence_threshold', explain: 'How sure the signals must be to override the default tier.', start: '0.5' },
      { name: 'capable_hold_turns', explain: 'Turns to stay on the capable model after escalating.', start: '2' },
    ],
  },
  {
    type: 'escalation',
    name: 'Classifier (escalation)',
    wizard: false,
    summary: 'Starts on the weak model, and a judge reviews each finished turn; after repeated trouble the session switches to the strong model for good.',
    how: [
      'The weak model answers every turn. The judge then rates the turn it actually produced. After a number of “escalate” verdicts in a row, the session latches to the strong model.',
      'It judges real work rather than predicting difficulty, so it fits long agent sessions that only sometimes go wrong.',
    ],
    models: 'Strong, weak and a judge',
    extraCalls: 'A judge call per turn until the session escalates',
    latency: 'The judge’s response time on each turn',
    goodFor: ['Long agent sessions where the weak model usually copes'],
    avoidWhen: ['Tools that send no session ID: the latch can’t hold'],
    settings: [{ name: 'escalation.confirmations', explain: 'Escalate verdicts in a row needed to switch.', start: '2' }],
  },
  {
    type: 'composite',
    name: 'Composite',
    wizard: false,
    summary: 'A classifier picks the default tier from the user’s message; the stage router then runs the tool steps.',
    how: [
      'When the person writes (or once per session), a judge sets the default tier. The stage router then handles the agent’s tool loop with its own signals until the next user message.',
    ],
    models: 'Two, plus a judge',
    extraCalls: 'One judge call per user message or session',
    latency: 'The judge’s response time once per user message',
    goodFor: ['Interactive coding agents where the request sets the difficulty and tool results refine it'],
    avoidWhen: ['Clients without a session ID, unless message_hash_fallback is on'],
    settings: [],
  },
  {
    type: 'plan_execute',
    name: 'Plan / execute',
    wizard: false,
    summary: 'The capable model inspects and plans; after the first file edit, the efficient model does the rest.',
    how: ['Read-only work stays on the capable model with a planning instruction. The first edit or write hands the whole session to the efficient model, and the choice holds for that session.'],
    models: 'Two: capable planner, efficient executor',
    extraCalls: 'None',
    latency: 'None added',
    goodFor: ['Coding tasks with a clear planning phase'],
    avoidWhen: ['Chat without tools: there’s never an edit to hand off at'],
    settings: [],
  },
  {
    type: 'advisor',
    name: 'Advisor gate',
    wizard: false,
    summary: 'One model does all the work; a stronger model reviews its plans and “done” claims and can send it back.',
    how: [
      'The executor serves every turn. When it presents a plan or claims to be finished, the advisor reviews the transcript and approves the turn or discards it with a concrete plan for the executor to follow. The advisor never answers the user directly.',
    ],
    models: 'Executor and advisor',
    extraCalls: 'A review at key moments, within a per-session budget',
    latency: 'A review’s response time at those moments',
    goodFor: ['Getting closer to strong-model quality while paying mostly for the cheaper model'],
    avoidWhen: ['Short questions: nothing to review'],
    settings: [],
  },
  {
    type: 'custom',
    name: 'Classifier (custom)',
    wizard: false,
    summary: 'Route among any number of models with your own judge instructions and groups.',
    how: [
      'You define groups of models (say fast, coding, long-context) and write the judge’s instructions and answer format. The judge names a group; its first model answers and the rest are fallbacks.',
    ],
    models: 'Any number, in groups you define',
    extraCalls: 'One judge call per decision',
    latency: 'The judge’s response time on every decision',
    goodFor: ['More than two models', 'Routing rules specific to your work'],
    avoidWhen: ['You don’t want to write and tune a judge prompt'],
    settings: [],
  },
]

export const byType = Object.fromEntries(strategies.map((s) => [s.type, s]))

// What we learned about judges running real traffic, shown on the Help page and in the
// classifier's help.
export const judgeAdvice = [
  'Fast and not a reasoning model. The judge runs before the answer, so its time adds to every decision. A reasoning model as judge can think for minutes (we measured a Qwen judge still running after 3 minutes; GPT-6 Luna took 18 s on a hard prompt; Haiku and Ministral took 2–4 s).',
  'Able to return structured JSON. Switchyard asks the judge for a JSON verdict through the provider’s structured-output feature.',
  'Reliable. If the judge’s provider fails or refuses a request, every route using that judge fails too. Test a new judge in the Playground before applying it to all routes.',
  'Calibrate the threshold per judge. Different judges rate the same task differently: with Ministral as judge, a hard Rust design question went to the weak model at threshold 0.4, while GPT-6 Luna sent it to the strong model.',
  'Cheap per decision, not just per token. Judge tokens show up as “Routing tokens” on the Usage page.',
]
