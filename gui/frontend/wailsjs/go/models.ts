export namespace backend {
	
	export class DetectedURL {
	    url: string;
	    albumUrl: string;
	    site: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new DetectedURL(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.albumUrl = source["albumUrl"];
	        this.site = source["site"];
	        this.error = source["error"];
	    }
	}
	export class Match {
	    row: number;
	    index: number;
	
	    static createFrom(source: any = {}) {
	        return new Match(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.row = source["row"];
	        this.index = source["index"];
	    }
	}
	export class Options {
	    sites: string[];
	    qualities: string[];
	    audioFormats: string[];
	    browsers: string[];
	    configPath: string;
	
	    static createFrom(source: any = {}) {
	        return new Options(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sites = source["sites"];
	        this.qualities = source["qualities"];
	        this.audioFormats = source["audioFormats"];
	        this.browsers = source["browsers"];
	        this.configPath = source["configPath"];
	    }
	}
	export class PreviewItem {
	    title: string;
	    size: number;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.size = source["size"];
	        this.url = source["url"];
	    }
	}
	export class PreviewAlbum {
	    url: string;
	    site: string;
	    slug: string;
	    items: PreviewItem[];
	    totalSize: number;
	    unknown: number;
	    warning: string;
	    diskWarning: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewAlbum(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.site = source["site"];
	        this.slug = source["slug"];
	        this.items = this.convertValues(source["items"], PreviewItem);
	        this.totalSize = source["totalSize"];
	        this.unknown = source["unknown"];
	        this.warning = source["warning"];
	        this.diskWarning = source["diskWarning"];
	        this.error = source["error"];
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
	
	export class RowView {
	    row: number;
	    index: number;
	    header: boolean;
	    album: string;
	    site: string;
	    title: string;
	    status: string;
	    bytes: number;
	    total: number;
	    speed: number;
	    queuePos: number;
	    folded: boolean;
	    done: number;
	    failed: number;
	    count: number;
	    error: string;
	    hasPath: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RowView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.row = source["row"];
	        this.index = source["index"];
	        this.header = source["header"];
	        this.album = source["album"];
	        this.site = source["site"];
	        this.title = source["title"];
	        this.status = source["status"];
	        this.bytes = source["bytes"];
	        this.total = source["total"];
	        this.speed = source["speed"];
	        this.queuePos = source["queuePos"];
	        this.folded = source["folded"];
	        this.done = source["done"];
	        this.failed = source["failed"];
	        this.count = source["count"];
	        this.error = source["error"];
	        this.hasPath = source["hasPath"];
	    }
	}
	export class RunInput {
	    text: string;
	    fromFile: boolean;
	    site: string;
	
	    static createFrom(source: any = {}) {
	        return new RunInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.fromFile = source["fromFile"];
	        this.site = source["site"];
	    }
	}
	export class StatsView {
	    total: number;
	    completed: number;
	    failed: number;
	    cancelled: number;
	    active: number;
	    pending: number;
	    speed: number;
	    doneBytes: number;
	    totalBytes: number;
	    unknown: number;
	    etaSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new StatsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.cancelled = source["cancelled"];
	        this.active = source["active"];
	        this.pending = source["pending"];
	        this.speed = source["speed"];
	        this.doneBytes = source["doneBytes"];
	        this.totalBytes = source["totalBytes"];
	        this.unknown = source["unknown"];
	        this.etaSeconds = source["etaSeconds"];
	    }
	}
	export class RunState {
	    running: boolean;
	    phase: string;
	    group: number;
	    groups: number;
	    site: string;
	    dest: string;
	    loadingErr: string;
	    status: string;
	    statusSeq: number;
	    filter: string;
	    visible: number;
	    stats: StatsView;
	    batchMode: boolean;
	    albumsTotal: number;
	    albumsScraped: number;
	    scrapingAlbum: string;
	    errors: number;
	    active: boolean;
	    watching: boolean;
	    nextRun: string;
	    version: number;
	
	    static createFrom(source: any = {}) {
	        return new RunState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.phase = source["phase"];
	        this.group = source["group"];
	        this.groups = source["groups"];
	        this.site = source["site"];
	        this.dest = source["dest"];
	        this.loadingErr = source["loadingErr"];
	        this.status = source["status"];
	        this.statusSeq = source["statusSeq"];
	        this.filter = source["filter"];
	        this.visible = source["visible"];
	        this.stats = this.convertValues(source["stats"], StatsView);
	        this.batchMode = source["batchMode"];
	        this.albumsTotal = source["albumsTotal"];
	        this.albumsScraped = source["albumsScraped"];
	        this.scrapingAlbum = source["scrapingAlbum"];
	        this.errors = source["errors"];
	        this.active = source["active"];
	        this.watching = source["watching"];
	        this.nextRun = source["nextRun"];
	        this.version = source["version"];
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
	export class Settings {
	    outputDir: string;
	    concurrency: number;
	    perHost: number;
	    rateLimit: string;
	    checksums: boolean;
	    proxy: string;
	    cookies: string;
	    cookiesFromBrowser: string;
	    quality: string;
	    youtubeBackend: string;
	    audioOnly: string;
	    include: string[];
	    exclude: string[];
	    mediaExtensions: string[];
	    minSize: string;
	    maxSize: string;
	    maxAlbumItems: number;
	    scrapeDelay: string;
	    timeout: string;
	    userAgent: string;
	    logFile: string;
	    diskCheck: boolean;
	    mediaOnly: boolean;
	    notify: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.outputDir = source["outputDir"];
	        this.concurrency = source["concurrency"];
	        this.perHost = source["perHost"];
	        this.rateLimit = source["rateLimit"];
	        this.checksums = source["checksums"];
	        this.proxy = source["proxy"];
	        this.cookies = source["cookies"];
	        this.cookiesFromBrowser = source["cookiesFromBrowser"];
	        this.quality = source["quality"];
	        this.youtubeBackend = source["youtubeBackend"];
	        this.audioOnly = source["audioOnly"];
	        this.include = source["include"];
	        this.exclude = source["exclude"];
	        this.mediaExtensions = source["mediaExtensions"];
	        this.minSize = source["minSize"];
	        this.maxSize = source["maxSize"];
	        this.maxAlbumItems = source["maxAlbumItems"];
	        this.scrapeDelay = source["scrapeDelay"];
	        this.timeout = source["timeout"];
	        this.userAgent = source["userAgent"];
	        this.logFile = source["logFile"];
	        this.diskCheck = source["diskCheck"];
	        this.mediaOnly = source["mediaOnly"];
	        this.notify = source["notify"];
	    }
	}

}

