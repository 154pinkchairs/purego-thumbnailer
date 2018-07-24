#include "video.h"
#include <libavutil/imgutils.h>
#include <libswscale/swscale.h>

/**
 * Potential thumbnail lookup filter to reduce the risk of an inappropriate
 * selection (such as a black frame) we could get with an absolute seek.
 *
 * Simplified version of algorithm by Vadim Zaliva <lord@crocodile.org>.
 * http://notbrainsurgery.livejournal.com/29773.html
 *
 * Adapted by Janis Petersons <bakape@gmail.com>
 */

#define HIST_SIZE (3 * 256)
#define MAX_FRAMES 100

// Compute sum-square deviation to estimate "closeness"
static double compute_error(
    const int hist[HIST_SIZE], const double median[HIST_SIZE])
{
    double sum_sq_err = 0;
    for (int i = 0; i < HIST_SIZE; i++) {
        const double err = median[i] - (double)hist[i];
        sum_sq_err += err * err;
    }
    return sum_sq_err;
}

// Select best frame based on RGB histograms
static int select_best_frame(AVFrame* frames[])
{
    int best_i = 0;
    // RGB color distribution histograms of the frames
    int hists[MAX_FRAMES][HIST_SIZE] = { 0 };

    // Compute each frame's histogram
    int frame_i;
    for (frame_i = 0; frame_i < MAX_FRAMES; frame_i++) {
        const AVFrame* f = frames[frame_i];
        if (!f || !f->data) {
            frame_i--;
            break;
        }
        uint8_t* p = f->data[0];
        for (int j = 0; j < f->height; j++) {
            for (int i = 0; i < f->width; i++) {
                for (int k = 0; k < 3; k++) {
                    hists[frame_i][k * 256 + p[i * 3 + k]]++;
                }
            }
            p += f->linesize[0];
        }
    }

    // Error on first frame or no frames at all
    if (frame_i == -1) {
        return -1;
    }

    // Average histograms of up to 100 frames
    double average[HIST_SIZE] = { 0 };
    for (int j = 0; j <= frame_i; j++) {
        for (int i = 0; i <= frame_i; i++) {
            average[j] = (double)hists[i][j];
        }
        average[j] /= frame_i + 1;
    }

    // Find the frame closer to the average using the sum of squared errors
    double min_sq_err = -1;
    for (int i = 0; i <= frame_i; i++) {
        const double sq_err = compute_error(hists[i], average);
        if (i == 0 || sq_err < min_sq_err) {
            best_i = i;
            min_sq_err = sq_err;
        }
    }

    return best_i;
}

// Encode frame to RGBA image
static int encode_frame(struct Buffer* img, const AVFrame const* frame)
{
    struct SwsContext* ctx;
    uint8_t* dst_data[1];
    int dst_linesize[1];

    ctx = sws_getContext(frame->width, frame->height, frame->format,
        frame->width, frame->height, AV_PIX_FMT_RGBA,
        SWS_BICUBIC | SWS_ACCURATE_RND, NULL, NULL, NULL);
    if (!ctx) {
        return AVERROR(ENOMEM);
    }

    img->width = (unsigned long)frame->width;
    img->height = (unsigned long)frame->height;
    img->size = (size_t)av_image_get_buffer_size(
        AV_PIX_FMT_RGBA, frame->width, frame->height, 1);
    img->data = dst_data[0] = malloc(img->size); // RGB have one plane
    dst_linesize[0] = 4 * img->width; // RGBA stride

    sws_scale(ctx, (const uint8_t* const*)frame->data, frame->linesize, 0,
        frame->height, (uint8_t * const*)dst_data, dst_linesize);

    sws_freeContext(ctx);
    return 0;
}

int extract_video_image(struct Buffer* img, AVFormatContext* avfc,
    AVCodecContext* avcc, const int stream)
{
    int err = 0;
    int frame_i = 0;
    AVPacket pkt;
    AVFrame* frames[MAX_FRAMES] = { 0 };

    // Read up to 100 frames
    while (1) {
        err = av_read_frame(avfc, &pkt);
        switch (err) {
        case 0:
            break;
        case -1:
            // I don't know why, but this happens for some AVI and OGG files
            // mid-read. If some frames were actually read, just silence the
            // error and select from those.
            if (frame_i && frames[0]->data) {
                err = 0;
            }
            goto end;
        default:
            goto end;
        }

        if (pkt.stream_index == stream) {
            err = avcodec_send_packet(avcc, &pkt);
            if (err < 0) {
                goto end;
            }

            if (!frames[frame_i]) {
                frames[frame_i] = av_frame_alloc();
                if (!frames[frame_i]) {
                    err = AVERROR(ENOMEM);
                    goto end;
                }
            }
            err = avcodec_receive_frame(avcc, frames[frame_i]);
            switch (err) {
            case 0:
                if (++frame_i == MAX_FRAMES) {
                    goto end;
                }
                break;
            case AVERROR(EAGAIN):
                break;
            default:
                goto end;
            }
        }
        av_packet_unref(&pkt);
    }

end:
    if (pkt.buf) {
        av_packet_unref(&pkt);
    }
    switch (err) {
    case AVERROR_EOF:
        err = 0;
    case 0:
        if (frame_i < 1) {
            err = -1;
        } else {
            const int best = select_best_frame(frames);
            if (best == -1) {
                err = -1;
            } else {
                err = encode_frame(img, frames[best]);
            }
        }
        break;
    }
    for (int i = 0; i < MAX_FRAMES; i++) {
        if (!frames[i]) {
            break;
        }
        if (frames[i]->data) {
            av_frame_free(&frames[i]);
        }
    }
    return err;
}
