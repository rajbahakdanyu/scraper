package backend

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

// scraperShowInFinder shows a folder in a Finder window, or with reveal set
// selects the item in its parent folder. Neither launches the item. AppKit
// work is done on the main thread. Returns 0 if path is not valid UTF-8.
static int scraperShowInFinder(_GoString_ path, int reveal) {
	@autoreleasepool {
		// Copied before returning: the Go string is only valid during the call.
		NSString *p = [[NSString alloc] initWithBytes:_GoStringPtr(path)
		                                       length:_GoStringLen(path)
		                                     encoding:NSUTF8StringEncoding];
		if (p == nil) {
			return 0;
		}
		dispatch_block_t work = ^{
			NSWorkspace *ws = [NSWorkspace sharedWorkspace];
			if (reveal) {
				[ws activateFileViewerSelectingURLs:@[ [NSURL fileURLWithPath:p] ]];
			} else {
				[ws selectFile:nil inFileViewerRootedAtPath:p];
			}
		};
		if ([NSThread isMainThread]) {
			work();
		} else {
			dispatch_async(dispatch_get_main_queue(), work);
		}
		return 1;
	}
}
*/
import "C"

import "errors"

// platformOpen uses NSWorkspace directly rather than running open(1).
func platformOpen(abs string, reveal bool) error {
	flag := C.int(0)
	if reveal {
		flag = 1
	}
	if C.scraperShowInFinder(abs, flag) == 0 {
		return errors.New("path is not valid UTF-8")
	}
	return nil
}
