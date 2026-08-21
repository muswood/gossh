export namespace agent {
	
	export class Approval {
	    taskId: string;
	    stepId: string;
	    toolName: string;
	    command: string;
	    purpose: string;
	    risk: string;
	    approvalLevel?: number;
	    // Go type: time
	    expiresAt?: any;
	    // Go type: time
	    requestedAt?: any;
	    requestedBy?: string;
	
	    static createFrom(source: any = {}) {
	        return new Approval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.stepId = source["stepId"];
	        this.toolName = source["toolName"];
	        this.command = source["command"];
	        this.purpose = source["purpose"];
	        this.risk = source["risk"];
	        this.approvalLevel = source["approvalLevel"];
	        this.expiresAt = this.convertValues(source["expiresAt"], null);
	        this.requestedAt = this.convertValues(source["requestedAt"], null);
	        this.requestedBy = source["requestedBy"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Event {
	    id?: string;
	    taskId: string;
	    stepId?: string;
	    type: string;
	    payload?: any;
	    // Go type: time
	    timestamp: any;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.taskId = source["taskId"];
	        this.stepId = source["stepId"];
	        this.type = source["type"];
	        this.payload = source["payload"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MutationPlan {
	    preconditionCommand: string;
	    snapshotCommand: string;
	    verifyCommand: string;
	    rollbackCommand: string;
	
	    static createFrom(source: any = {}) {
	        return new MutationPlan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preconditionCommand = source["preconditionCommand"];
	        this.snapshotCommand = source["snapshotCommand"];
	        this.verifyCommand = source["verifyCommand"];
	        this.rollbackCommand = source["rollbackCommand"];
	    }
	}
	export class PolicyDecision {
	    allowed: boolean;
	    reason?: string;
	    risk?: string;
	    readOnly: boolean;
	    mutating: boolean;
	    deleting: boolean;
	    administrator?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PolicyDecision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allowed = source["allowed"];
	        this.reason = source["reason"];
	        this.risk = source["risk"];
	        this.readOnly = source["readOnly"];
	        this.mutating = source["mutating"];
	        this.deleting = source["deleting"];
	        this.administrator = source["administrator"];
	    }
	}
	export class RecoveryManifest {
	    generation: number;
	    // Go type: time
	    capturedAt: any;
	    lastEventId?: string;
	    completedStepIds?: string[];
	    replayStepIds?: string[];
	    replayIdempotencyKeys?: string[];
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new RecoveryManifest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generation = source["generation"];
	        this.capturedAt = this.convertValues(source["capturedAt"], null);
	        this.lastEventId = source["lastEventId"];
	        this.completedStepIds = source["completedStepIds"];
	        this.replayStepIds = source["replayStepIds"];
	        this.replayIdempotencyKeys = source["replayIdempotencyKeys"];
	        this.reason = source["reason"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReportEvidence {
	    id: string;
	    toolName?: string;
	    stepId?: string;
	    targetId?: string;
	    command?: string;
	    source?: string;
	    output?: string;
	    exitCode?: number;
	
	    static createFrom(source: any = {}) {
	        return new ReportEvidence(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.toolName = source["toolName"];
	        this.stepId = source["stepId"];
	        this.targetId = source["targetId"];
	        this.command = source["command"];
	        this.source = source["source"];
	        this.output = source["output"];
	        this.exitCode = source["exitCode"];
	    }
	}
	export class ReportFinding {
	    title: string;
	    description: string;
	    severity?: string;
	    evidenceIds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ReportFinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.description = source["description"];
	        this.severity = source["severity"];
	        this.evidenceIds = source["evidenceIds"];
	    }
	}
	export class Report {
	    title: string;
	    summary: string;
	    severity: string;
	    findings?: ReportFinding[];
	    evidence?: ReportEvidence[];
	    recommendations?: string[];
	    executedSteps?: string[];
	    limitations?: string[];
	    custom?: Record<string, any>;
	    // Go type: time
	    generatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.summary = source["summary"];
	        this.severity = source["severity"];
	        this.findings = this.convertValues(source["findings"], ReportFinding);
	        this.evidence = this.convertValues(source["evidence"], ReportEvidence);
	        this.recommendations = source["recommendations"];
	        this.executedSteps = source["executedSteps"];
	        this.limitations = source["limitations"];
	        this.custom = source["custom"];
	        this.generatedAt = this.convertValues(source["generatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class SecurityConfig {
	    whitelistEnabled: boolean;
	    blacklistEnabled: boolean;
	    mutationsEnabled: boolean;
	    deletionsEnabled: boolean;
	    administratorEnabled: boolean;
	    readOnlyNoApproval: boolean;
	    commandWhitelist: string[];
	    commandBlacklist: string[];
	
	    static createFrom(source: any = {}) {
	        return new SecurityConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.whitelistEnabled = source["whitelistEnabled"];
	        this.blacklistEnabled = source["blacklistEnabled"];
	        this.mutationsEnabled = source["mutationsEnabled"];
	        this.deletionsEnabled = source["deletionsEnabled"];
	        this.administratorEnabled = source["administratorEnabled"];
	        this.readOnlyNoApproval = source["readOnlyNoApproval"];
	        this.commandWhitelist = source["commandWhitelist"];
	        this.commandBlacklist = source["commandBlacklist"];
	    }
	}
	export class WorkflowStep {
	    id: string;
	    title: string;
	    when?: string;
	    prompt: string;
	    allowedTools?: string[];
	    repeat?: number;
	    maxAttempts?: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.when = source["when"];
	        this.prompt = source["prompt"];
	        this.allowedTools = source["allowedTools"];
	        this.repeat = source["repeat"];
	        this.maxAttempts = source["maxAttempts"];
	    }
	}
	export class Target {
	    id: string;
	    sessionId: string;
	    name?: string;
	    host?: string;
	
	    static createFrom(source: any = {}) {
	        return new Target(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.name = source["name"];
	        this.host = source["host"];
	    }
	}
	export class StartRequest {
	    id: string;
	    sessionId: string;
	    transport?: string;
	    tabId: string;
	    targets?: Target[];
	    goal: string;
	    mode: string;
	    context: string;
	    history: ai.Message[];
	    autonomous: boolean;
	    allowMutations: boolean;
	    maxSteps: number;
	    recoveryCount?: number;
	    skillId?: string;
	    skillVersion?: string;
	    skillIntegrityHash?: string;
	    skillPrompt?: string;
	    skillParameters?: Record<string, any>;
	    allowedTools?: string[];
	    timeoutSeconds?: number;
	    dryRun?: boolean;
	    targetParameters?: Record<string, any>;
	    skillWorkflow?: string;
	    reportTemplate?: string;
	    workflow?: WorkflowStep[];
	    workflowAttempts?: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new StartRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.transport = source["transport"];
	        this.tabId = source["tabId"];
	        this.targets = this.convertValues(source["targets"], Target);
	        this.goal = source["goal"];
	        this.mode = source["mode"];
	        this.context = source["context"];
	        this.history = this.convertValues(source["history"], ai.Message);
	        this.autonomous = source["autonomous"];
	        this.allowMutations = source["allowMutations"];
	        this.maxSteps = source["maxSteps"];
	        this.recoveryCount = source["recoveryCount"];
	        this.skillId = source["skillId"];
	        this.skillVersion = source["skillVersion"];
	        this.skillIntegrityHash = source["skillIntegrityHash"];
	        this.skillPrompt = source["skillPrompt"];
	        this.skillParameters = source["skillParameters"];
	        this.allowedTools = source["allowedTools"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	        this.dryRun = source["dryRun"];
	        this.targetParameters = source["targetParameters"];
	        this.skillWorkflow = source["skillWorkflow"];
	        this.reportTemplate = source["reportTemplate"];
	        this.workflow = this.convertValues(source["workflow"], WorkflowStep);
	        this.workflowAttempts = source["workflowAttempts"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ToolResult {
	    toolName: string;
	    command?: string;
	    output?: string;
	    exitCode: number;
	    durationMillis: number;
	    attempts?: number;
	    error?: string;
	    redacted: boolean;
	    metadata?: Record<string, any>;
	    status?: string;
	    errorKind?: string;
	    timedOut?: boolean;
	    cancelled?: boolean;
	    targetId?: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.toolName = source["toolName"];
	        this.command = source["command"];
	        this.output = source["output"];
	        this.exitCode = source["exitCode"];
	        this.durationMillis = source["durationMillis"];
	        this.attempts = source["attempts"];
	        this.error = source["error"];
	        this.redacted = source["redacted"];
	        this.metadata = source["metadata"];
	        this.status = source["status"];
	        this.errorKind = source["errorKind"];
	        this.timedOut = source["timedOut"];
	        this.cancelled = source["cancelled"];
	        this.targetId = source["targetId"];
	    }
	}
	export class Step {
	    id: string;
	    taskId: string;
	    kind: string;
	    toolName?: string;
	    arguments?: Record<string, any>;
	    purpose?: string;
	    risk?: string;
	    status: string;
	    approved?: boolean;
	    result?: ToolResult;
	    // Go type: time
	    startedAt?: any;
	    // Go type: time
	    finishedAt?: any;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	    idempotencyKey?: string;
	    leaseOwner?: string;
	    // Go type: time
	    leaseUntil?: any;
	    // Go type: time
	    heartbeatAt?: any;
	    attempt?: number;
	    timeoutMillis?: number;
	    mutationPlan?: MutationPlan;
	
	    static createFrom(source: any = {}) {
	        return new Step(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.taskId = source["taskId"];
	        this.kind = source["kind"];
	        this.toolName = source["toolName"];
	        this.arguments = source["arguments"];
	        this.purpose = source["purpose"];
	        this.risk = source["risk"];
	        this.status = source["status"];
	        this.approved = source["approved"];
	        this.result = this.convertValues(source["result"], ToolResult);
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.idempotencyKey = source["idempotencyKey"];
	        this.leaseOwner = source["leaseOwner"];
	        this.leaseUntil = this.convertValues(source["leaseUntil"], null);
	        this.heartbeatAt = this.convertValues(source["heartbeatAt"], null);
	        this.attempt = source["attempt"];
	        this.timeoutMillis = source["timeoutMillis"];
	        this.mutationPlan = this.convertValues(source["mutationPlan"], MutationPlan);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Task {
	    id: string;
	    sessionId: string;
	    transport?: string;
	    tabId: string;
	    targets?: Target[];
	    goal: string;
	    mode: string;
	    context?: string;
	    conversationContext?: string;
	    history?: ai.Message[];
	    autonomous: boolean;
	    allowMutations: boolean;
	    maxSteps: number;
	    recoveryCount?: number;
	    skillId?: string;
	    skillVersion?: string;
	    skillIntegrityHash?: string;
	    skillPrompt?: string;
	    skillParameters?: Record<string, any>;
	    allowedTools?: string[];
	    timeoutSeconds?: number;
	    dryRun?: boolean;
	    targetParameters?: Record<string, any>;
	    skillWorkflow?: string;
	    reportTemplate?: string;
	    workflow?: WorkflowStep[];
	    workflowIndex?: number;
	    workflowAttempts?: Record<string, number>;
	    runnerOwner?: string;
	    // Go type: time
	    runnerLeaseUntil?: any;
	    // Go type: time
	    runnerHeartbeatAt?: any;
	    status: string;
	    currentStep: number;
	    pendingApproval?: Approval;
	    result?: string;
	    report?: Report;
	    error?: string;
	    persistenceState?: string;
	    persistenceError?: string;
	    persistenceFailures?: number;
	    // Go type: time
	    persistenceLastAttemptAt?: any;
	    steps?: Step[];
	    events?: Event[];
	    recoveryManifest?: RecoveryManifest;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.transport = source["transport"];
	        this.tabId = source["tabId"];
	        this.targets = this.convertValues(source["targets"], Target);
	        this.goal = source["goal"];
	        this.mode = source["mode"];
	        this.context = source["context"];
	        this.conversationContext = source["conversationContext"];
	        this.history = this.convertValues(source["history"], ai.Message);
	        this.autonomous = source["autonomous"];
	        this.allowMutations = source["allowMutations"];
	        this.maxSteps = source["maxSteps"];
	        this.recoveryCount = source["recoveryCount"];
	        this.skillId = source["skillId"];
	        this.skillVersion = source["skillVersion"];
	        this.skillIntegrityHash = source["skillIntegrityHash"];
	        this.skillPrompt = source["skillPrompt"];
	        this.skillParameters = source["skillParameters"];
	        this.allowedTools = source["allowedTools"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	        this.dryRun = source["dryRun"];
	        this.targetParameters = source["targetParameters"];
	        this.skillWorkflow = source["skillWorkflow"];
	        this.reportTemplate = source["reportTemplate"];
	        this.workflow = this.convertValues(source["workflow"], WorkflowStep);
	        this.workflowIndex = source["workflowIndex"];
	        this.workflowAttempts = source["workflowAttempts"];
	        this.runnerOwner = source["runnerOwner"];
	        this.runnerLeaseUntil = this.convertValues(source["runnerLeaseUntil"], null);
	        this.runnerHeartbeatAt = this.convertValues(source["runnerHeartbeatAt"], null);
	        this.status = source["status"];
	        this.currentStep = source["currentStep"];
	        this.pendingApproval = this.convertValues(source["pendingApproval"], Approval);
	        this.result = source["result"];
	        this.report = this.convertValues(source["report"], Report);
	        this.error = source["error"];
	        this.persistenceState = source["persistenceState"];
	        this.persistenceError = source["persistenceError"];
	        this.persistenceFailures = source["persistenceFailures"];
	        this.persistenceLastAttemptAt = this.convertValues(source["persistenceLastAttemptAt"], null);
	        this.steps = this.convertValues(source["steps"], Step);
	        this.events = this.convertValues(source["events"], Event);
	        this.recoveryManifest = this.convertValues(source["recoveryManifest"], RecoveryManifest);
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

export namespace ai {
	
	export class ChatToolCallFunction {
	    name: string;
	    arguments: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatToolCallFunction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.arguments = source["arguments"];
	    }
	}
	export class ChatToolCall {
	    id: string;
	    type: string;
	    function: ChatToolCallFunction;
	
	    static createFrom(source: any = {}) {
	        return new ChatToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.function = this.convertValues(source["function"], ChatToolCallFunction);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Message {
	    role: string;
	    content: string;
	    tool_calls?: ChatToolCall[];
	    tool_call_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.tool_calls = this.convertValues(source["tool_calls"], ChatToolCall);
	        this.tool_call_id = source["tool_call_id"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace config {
	
	export class AIProviderConfig {
	    provider: string;
	    model: string;
	    embeddingModel?: string;
	    apiKey: string;
	    baseURL: string;
	    apiMode?: string;
	    ragEnabled: boolean;
	    ragVectorBackend?: string;
	    ragVectorEndpoint?: string;
	    ragVectorCollection?: string;
	    ragVectorApiKey?: string;
	    maxTokens: number;
	    temperature: number;
	    agentMaxSteps: number;
	
	    static createFrom(source: any = {}) {
	        return new AIProviderConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.embeddingModel = source["embeddingModel"];
	        this.apiKey = source["apiKey"];
	        this.baseURL = source["baseURL"];
	        this.apiMode = source["apiMode"];
	        this.ragEnabled = source["ragEnabled"];
	        this.ragVectorBackend = source["ragVectorBackend"];
	        this.ragVectorEndpoint = source["ragVectorEndpoint"];
	        this.ragVectorCollection = source["ragVectorCollection"];
	        this.ragVectorApiKey = source["ragVectorApiKey"];
	        this.maxTokens = source["maxTokens"];
	        this.temperature = source["temperature"];
	        this.agentMaxSteps = source["agentMaxSteps"];
	    }
	}
	export class ConnectionGroup {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class ConnectionRecord {
	    id: string;
	    name: string;
	    protocol?: string;
	    host: string;
	    port: number;
	    username: string;
	    authMethod: string;
	    password?: string;
	    privateKey?: string;
	    privateKeyPath?: string;
	    certificatePath?: string;
	    passphrase?: string;
	    jumpHost?: string;
	    proxyType?: string;
	    proxyHost?: string;
	    proxyUsername?: string;
	    proxyPassword?: string;
	    proxyCommand?: string;
	    encoding: string;
	    startupCmd?: string;
	    keepAliveSeconds: number;
	    terminalTheme?: string;
	    serialBaudRate?: number;
	    serialDataBits?: number;
	    serialStopBits?: number;
	    serialParity?: string;
	    serialAutoReconnect?: boolean;
	    groupId: string;
	    starred: boolean;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.authMethod = source["authMethod"];
	        this.password = source["password"];
	        this.privateKey = source["privateKey"];
	        this.privateKeyPath = source["privateKeyPath"];
	        this.certificatePath = source["certificatePath"];
	        this.passphrase = source["passphrase"];
	        this.jumpHost = source["jumpHost"];
	        this.proxyType = source["proxyType"];
	        this.proxyHost = source["proxyHost"];
	        this.proxyUsername = source["proxyUsername"];
	        this.proxyPassword = source["proxyPassword"];
	        this.proxyCommand = source["proxyCommand"];
	        this.encoding = source["encoding"];
	        this.startupCmd = source["startupCmd"];
	        this.keepAliveSeconds = source["keepAliveSeconds"];
	        this.terminalTheme = source["terminalTheme"];
	        this.serialBaudRate = source["serialBaudRate"];
	        this.serialDataBits = source["serialDataBits"];
	        this.serialStopBits = source["serialStopBits"];
	        this.serialParity = source["serialParity"];
	        this.serialAutoReconnect = source["serialAutoReconnect"];
	        this.groupId = source["groupId"];
	        this.starred = source["starred"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class ConnectRequest {
	    id: string;
	    name: string;
	    host: string;
	    port: number;
	    username: string;
	    authMethod: string;
	    password: string;
	    privateKey: string;
	    privateKeyPath: string;
	    certificatePath: string;
	    passphrase: string;
	    jumpHost: string;
	    proxyType: string;
	    proxyHost: string;
	    proxyUsername: string;
	    proxyPassword: string;
	    proxyCommand: string;
	    encoding: string;
	    startupCmd: string;
	    keepAliveSeconds: number;
	    cols: number;
	    rows: number;
	
	    static createFrom(source: any = {}) {
	        return new ConnectRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.authMethod = source["authMethod"];
	        this.password = source["password"];
	        this.privateKey = source["privateKey"];
	        this.privateKeyPath = source["privateKeyPath"];
	        this.certificatePath = source["certificatePath"];
	        this.passphrase = source["passphrase"];
	        this.jumpHost = source["jumpHost"];
	        this.proxyType = source["proxyType"];
	        this.proxyHost = source["proxyHost"];
	        this.proxyUsername = source["proxyUsername"];
	        this.proxyPassword = source["proxyPassword"];
	        this.proxyCommand = source["proxyCommand"];
	        this.encoding = source["encoding"];
	        this.startupCmd = source["startupCmd"];
	        this.keepAliveSeconds = source["keepAliveSeconds"];
	        this.cols = source["cols"];
	        this.rows = source["rows"];
	    }
	}
	export class MCPServerRequest {
	    id: string;
	    transport?: string;
	    endpoint?: string;
	    command: string;
	    args?: string[];
	    env?: string[];
	    authToken?: string;
	    oauthAccessToken?: string;
	    allowedTools?: string[];
	    targetIds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new MCPServerRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.transport = source["transport"];
	        this.endpoint = source["endpoint"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	        this.authToken = source["authToken"];
	        this.oauthAccessToken = source["oauthAccessToken"];
	        this.allowedTools = source["allowedTools"];
	        this.targetIds = source["targetIds"];
	    }
	}
	export class PortForwardRequest {
	    sessionId: string;
	    id: string;
	    type: string;
	    localHost: string;
	    localPort: number;
	    remoteHost: string;
	    remotePort: number;
	
	    static createFrom(source: any = {}) {
	        return new PortForwardRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.id = source["id"];
	        this.type = source["type"];
	        this.localHost = source["localHost"];
	        this.localPort = source["localPort"];
	        this.remoteHost = source["remoteHost"];
	        this.remotePort = source["remotePort"];
	    }
	}
	export class RAGDocumentRequest {
	    title: string;
	    content: string;
	    source?: string;
	    tags?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RAGDocumentRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.content = source["content"];
	        this.source = source["source"];
	        this.tags = source["tags"];
	    }
	}
	export class TCPConnectRequest {
	    id: string;
	    host: string;
	    port: number;
	    protocol: string;
	
	    static createFrom(source: any = {}) {
	        return new TCPConnectRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	    }
	}
	export class sessionLogReadResult {
	    data: string;
	    nextOffset: number;
	    eof: boolean;
	
	    static createFrom(source: any = {}) {
	        return new sessionLogReadResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.data = source["data"];
	        this.nextOffset = source["nextOffset"];
	        this.eof = source["eof"];
	    }
	}

}

export namespace serial {
	
	export class Config {
	    portName: string;
	    baudRate: number;
	    dataBits: number;
	    stopBits: number;
	    parity: string;
	    hexMode: boolean;
	    autoReconnect: boolean;
	    encoding: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.portName = source["portName"];
	        this.baudRate = source["baudRate"];
	        this.dataBits = source["dataBits"];
	        this.stopBits = source["stopBits"];
	        this.parity = source["parity"];
	        this.hexMode = source["hexMode"];
	        this.autoReconnect = source["autoReconnect"];
	        this.encoding = source["encoding"];
	    }
	}

}

