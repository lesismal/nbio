#import <Foundation/Foundation.h>

#include <unistd.h>
#include <getopt.h>

#include <string>
#include <thread>
#include <vector>

std::string gUrl;
std::string gBearerToken;
std::string gCAPath;
std::vector<uint8_t> gMessageVec;

@interface HttpClientSession : NSObject <NSURLSessionDelegate>
- (id) init : (std::string) base_url capath:(std::string)capath;
- (void) startWebsocketSession;
-(void) socketRunLoop: (NSURLSession*)session task:(NSURLSessionWebSocketTask*) task protocol:(NSString*) protocol;
  @property std::string base_url;
  @property NSData *capem;
@end

@implementation HttpClientSession

-(id) init : (std::string) base_url capath:(std::string)capath {
    self.base_url = base_url;
    NSError * error;
    self.capem = [NSData dataWithContentsOfFile:[NSString stringWithUTF8String: capath.c_str()]  options:0 error:&error];

    return self;
}





- (void) startWebsocketSession
{
    NSURL *url = [NSURL URLWithString: [NSString stringWithUTF8String: gUrl.c_str()]];

    NSURLSessionConfiguration *defaultConfigObject = [NSURLSessionConfiguration ephemeralSessionConfiguration];
    NSDictionary *hdrDict = [NSMutableDictionary dictionary];

    std::string bs = "Bearer " + gBearerToken;
    NSString *s = [NSString stringWithUTF8String: bs.c_str()];
    [hdrDict setValue:s forKey: @"Authorization"];
    [defaultConfigObject setHTTPAdditionalHeaders: hdrDict];
    NSURLSession *defaultSession = [NSURLSession sessionWithConfiguration: defaultConfigObject delegate: self delegateQueue: [NSOperationQueue mainQueue]];
    
    NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];
    NSURLSessionWebSocketTask* wsTask = [defaultSession webSocketTaskWithRequest: request];
    [wsTask resume];
}


- (void)socketRunLoop:(NSURLSession*)session task:(NSURLSessionWebSocketTask*)task protocol:(NSString*)protocol {

    for (int i=0; i < 3; i++) {
        [task receiveMessageWithCompletionHandler:^(NSURLSessionWebSocketMessage * _Nullable message, NSError * _Nullable error) {
                if (nil != error) {
                    NSLog(@"ws receive message err: %@", error);
                } else if (nil != message) {
                    if (nil != message.data) {
                        NSLog(@"received message %d bytes %s",(int32_t)message.data.length, (error != nil ? "ERROR": ""));
                    } else {
                        NSLog(@"received message %@ %s",[message string], (error != nil ? "ERROR": ""));
                    }
                }
        }];

        NSData *data = [NSData dataWithBytes: gMessageVec.data() length: gMessageVec.size()];
        auto msg = [[NSURLSessionWebSocketMessage alloc] initWithData: data];
        [task sendMessage: msg completionHandler:^(NSError * _Nullable error) {
          if (error != nil) {
            NSLog(@"ERROR sending %d bytes: %@", (int)gMessageVec.size(), error);

          } else {
            NSLog(@"sent message %d bytes", (int)gMessageVec.size());
          }
        }];
        usleep(5000000);
    }

}

#pragma mark - NSURLSessionDelegate
- (void)URLSession:(NSURLSession*)session didBecomeInvalidWithError:(NSError*)error {
  NSLog(@"Session Invalidated: %@", error);
}
- (void)URLSession:(NSURLSession*)session task:(NSURLSessionTask*)task didCompleteWithError:(NSError*)error {
  NSLog(@"Task Completed: %@, %@", task, error);
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition disposition, NSURLCredential *credential))completionHandler
{
    NSLog(@"didReceiveChallenge method:%@", challenge.protectionSpace.authenticationMethod);
    
    NSURLProtectionSpace *protectionSpace = [challenge protectionSpace];
    if ([protectionSpace authenticationMethod] == NSURLAuthenticationMethodServerTrust) {
        SecTrustRef trust = [protectionSpace serverTrust];

        if (0 < self.capem.length) {
            // Add trusted certs
         
            SecExternalItemType itemType = kSecItemTypeAggregate;
            SecItemImportExportKeyParameters params {0};
            CFArrayRef items;
            OSStatus status = SecItemImport((__bridge CFDataRef)self.capem, NULL /* optFilename */, NULL /* optFormat */, &itemType, 0 /* flags */, &params, NULL, &items);
            if (errSecSuccess == status) {

                SecTrustSetAnchorCertificates(trust, items);
                SecTrustSetAnchorCertificatesOnly(trust, false);

                /* Re-evaluate the trust policy. */
            
                CFErrorRef evalErr;
                if (NO == SecTrustEvaluateWithError(trust, &evalErr)) {
                    long errCode = CFErrorGetCode(evalErr);
                    // Trust evaluation failed.
                    NSLog(@"Server Cert not trusted %ld", errCode);
         
                    //[connection cancel];
                    completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, nil);
         
                    // Perform other cleanup here, as needed.
                    return;
                } else {
                    // Success
                }
            } else {
                NSLog(@"failed to import certs");
                //[connection cancel];
                completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, nil);
            }
        }

    }
    
 
    completionHandler(NSURLSessionAuthChallengeUseCredential, [NSURLCredential credentialForTrust:challenge.protectionSpace.serverTrust]);
}
#pragma mark - NSURLSessionStreamDelegate
- (void)URLSession:(NSURLSession*)session webSocketTask:(NSURLSessionWebSocketTask*)task didOpenWithProtocol:(NSString*)protocol {
  NSLog(@"WebSocket Opened: %@, %@", task, protocol);
    dispatch_async(dispatch_get_main_queue(), ^{
        [self socketRunLoop: session task: task protocol: protocol];
    });
}


- (void)URLSession:(NSURLSession*)session webSocketTask:(NSURLSessionWebSocketTask*)task didCloseWithCode:(NSURLSessionWebSocketCloseCode)closeCode reason:(NSData*)reason {
  NSLog(@"WebSocket Closed: %@, %ld, %@", task, (long)closeCode, reason);
}

@end




void clientRunLoop(std::string base_url, std::string capath) {
    HttpClientSession* st = [[HttpClientSession alloc] init : base_url capath: capath ];

    [st startWebsocketSession];

}

    static int readFileContentsBin(std::string path, std::vector<uint8_t> &dest)
    {
        int retval = 0;
        size_t offset = dest.size();
        dest.resize(offset + 256000);  // TODO: load file size

        FILE *fin = fopen(path.c_str(), "rb");
        if (nullptr == fin)
        {
            return EPERM;
        }

        size_t total = 0;
        uint8_t *p = dest.data() + offset;
        std::vector<uint8_t> buf(8192);
        while (!feof(fin))
        {
            size_t num_bytes_read = fread(buf.data(), 1, buf.size(), fin);
            if (0 == num_bytes_read)
            {
                break;
            }
            memcpy(p, buf.data(), num_bytes_read);
            p += num_bytes_read;
            total += num_bytes_read;
        }
        fclose(fin);
        dest.resize(total);
        return retval;
    }
    int parse_args(int argc, char **argv)
    {
        int c;

        while (1)
        {
            static struct option long_options[] = { /* These options set a flag. */
                //{ "verbose", no_argument, &gFlagVerbose, 1 },
                /* These options don’t set a flag.
                   We distinguish them by their indices. */
                { "bearer_token", required_argument, 0, 't' },
                { "url", required_argument, 0, 'u' },
                { "capath", required_argument, 0, 'c' },
                { "message_file", required_argument, 0, 'm' },
                { 0, 0, 0, 0 }
            };
            /* getopt_long stores the option index here. */
            int option_index = 0;

            c = getopt_long(argc, argv, "t:u:c:m:", long_options, &option_index);

            /* Detect the end of the options. */
            if (c == -1)
                break;

            switch (c)
            {
            case 0:
                /* If this option set a flag, do nothing else now. */
                if (long_options[option_index].flag != 0)
                    break;
                printf("option %s", long_options[option_index].name);
                if (optarg)
                    printf(" with arg %s", optarg);
                printf("\n");
                break;

            case 'c':
                gCAPath = std::string(optarg);
                break;
            case 't': {
                gBearerToken = std::string(optarg);
                break;
            }
            case 'u':
                gUrl = std::string(optarg);
                break;
            case 'm': {
                int status = readFileContentsBin(optarg, gMessageVec);
                if (0 != status) {
                    fprintf(stderr, "Unable to read message file '%s' errcode:%d\n", optarg, status);
                    exit(3);
                }
                break;
            }
            default:
                abort();
                return -1;
            }
        }

        return 0;
    }


void bgfunc() {
    clientRunLoop(gUrl,gCAPath);
}

int main(int argc, char * argv[]) {
    parse_args(argc, argv);
    
    if (gUrl.empty() || gMessageVec.empty()) {
        fprintf(stderr, "missing url or message file\n");
        return 4;
    }

    @autoreleasepool {

        std::thread t = std::thread(&bgfunc);
        dispatch_main();
    }
    return 0;
}
