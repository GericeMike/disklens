export namespace history {
	
	export class Delta {
	    Path: string;
	    Old: number;
	    New: number;
	    Diff: number;
	
	    static createFrom(source: any = {}) {
	        return new Delta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Path = source["Path"];
	        this.Old = source["Old"];
	        this.New = source["New"];
	        this.Diff = source["Diff"];
	    }
	}

}

export namespace kb {
	
	export class CleanItem {
	    sub: string;
	    risk: string;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new CleanItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sub = source["sub"];
	        this.risk = source["risk"];
	        this.note = source["note"];
	    }
	}
	export class CleanPath {
	    path: string;
	    size: number;
	    files: number;
	
	    static createFrom(source: any = {}) {
	        return new CleanPath(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	        this.files = source["files"];
	    }
	}
	export class CleanSummaryEntry {
	    entryId: string;
	    name: string;
	    cat: string;
	    risk: string;
	    why: string;
	    totalSize: number;
	    paths: CleanPath[];
	    clean?: CleanItem[];
	
	    static createFrom(source: any = {}) {
	        return new CleanSummaryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entryId = source["entryId"];
	        this.name = source["name"];
	        this.cat = source["cat"];
	        this.risk = source["risk"];
	        this.why = source["why"];
	        this.totalSize = source["totalSize"];
	        this.paths = this.convertValues(source["paths"], CleanPath);
	        this.clean = this.convertValues(source["clean"], CleanItem);
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
	export class RelatedPath {
	    paths: string[];
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new RelatedPath(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.note = source["note"];
	    }
	}

}

export namespace main {
	
	export class DriveOption {
	    letter: string;
	    total: number;
	    free: number;
	    current: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DriveOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.letter = source["letter"];
	        this.total = source["total"];
	        this.free = source["free"];
	        this.current = source["current"];
	    }
	}
	export class DriveStat {
	    letter: string;
	    total: number;
	    free: number;
	    usedPct: number;
	    atRisk: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DriveStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.letter = source["letter"];
	        this.total = source["total"];
	        this.free = source["free"];
	        this.usedPct = source["usedPct"];
	        this.atRisk = source["atRisk"];
	    }
	}
	export class FileRow {
	    path: string;
	    size: number;
	    modTime: string;
	
	    static createFrom(source: any = {}) {
	        return new FileRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	        this.modTime = source["modTime"];
	    }
	}
	export class HistoryItem {
	    index: number;
	    time: string;
	    scope: string[];
	    total: number;
	    duration: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.time = source["time"];
	        this.scope = source["scope"];
	        this.total = source["total"];
	        this.duration = source["duration"];
	    }
	}
	export class KBHitOut {
	    entryId: string;
	    name: string;
	    cat: string;
	    risk: string;
	    desc: string;
	    why: string;
	    exists: string[];
	    sizes: Record<string, number>;
	    clean?: kb.CleanItem[];
	    related?: kb.RelatedPath[];
	
	    static createFrom(source: any = {}) {
	        return new KBHitOut(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entryId = source["entryId"];
	        this.name = source["name"];
	        this.cat = source["cat"];
	        this.risk = source["risk"];
	        this.desc = source["desc"];
	        this.why = source["why"];
	        this.exists = source["exists"];
	        this.sizes = source["sizes"];
	        this.clean = this.convertValues(source["clean"], kb.CleanItem);
	        this.related = this.convertValues(source["related"], kb.RelatedPath);
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
	export class TypeRow {
	    category: string;
	    bytes: number;
	    files: number;
	
	    static createFrom(source: any = {}) {
	        return new TypeRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.category = source["category"];
	        this.bytes = source["bytes"];
	        this.files = source["files"];
	    }
	}
	export class OverviewData {
	    ready: boolean;
	    durationSec: number;
	    drives: DriveStat[];
	    totalBytes: number;
	    totalFiles: number;
	    totalDirs: number;
	    errors: number;
	    finishedAt: string;
	    finding: string;
	    topFiles: FileRow[];
	    typeStats: TypeRow[];
	    scanTime: string;
	
	    static createFrom(source: any = {}) {
	        return new OverviewData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ready = source["ready"];
	        this.durationSec = source["durationSec"];
	        this.drives = this.convertValues(source["drives"], DriveStat);
	        this.totalBytes = source["totalBytes"];
	        this.totalFiles = source["totalFiles"];
	        this.totalDirs = source["totalDirs"];
	        this.errors = source["errors"];
	        this.finishedAt = source["finishedAt"];
	        this.finding = source["finding"];
	        this.topFiles = this.convertValues(source["topFiles"], FileRow);
	        this.typeStats = this.convertValues(source["typeStats"], TypeRow);
	        this.scanTime = source["scanTime"];
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
	export class TreeNode {
	    path: string;
	    name: string;
	    bytes: number;
	    files: number;
	    isDir: boolean;
	    risk?: string;
	    kbName?: string;
	
	    static createFrom(source: any = {}) {
	        return new TreeNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.bytes = source["bytes"];
	        this.files = source["files"];
	        this.isDir = source["isDir"];
	        this.risk = source["risk"];
	        this.kbName = source["kbName"];
	    }
	}

}

